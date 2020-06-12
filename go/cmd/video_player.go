package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/3d0c/gmf"
	flutter "github.com/go-flutter-desktop/go-flutter"
	"github.com/go-flutter-desktop/go-flutter/plugin"
	"github.com/hajimehoshi/oto"

	_ "net/http/pprof"
)

func init() {
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()
}

const bufferSize = 1024 * 10
const channelName = "dev.flutter.pigeon.VideoPlayerApi."
const speakerChannels = 1
const speakerSampleRate = 44100
const speakerBufferSize = speakerSampleRate * speakerChannels * 2 / 10

var speakerCtx *oto.Context

// VideoPlayerPlugin implements flutter.Plugin and handles the host side of
// the official Dart Video Player plugin for Flutter.
// VideoPlayerPlugin contains multiple players.
type VideoPlayerPlugin struct {
	textureRegistry  *flutter.TextureRegistry
	messenger        plugin.BinaryMessenger
	videoPlayersLock sync.RWMutex
	videoPlayers     map[int32]*VideoPlayer
}

var _ flutter.Plugin = &VideoPlayerPlugin{} // compile-time type check

// InitPlugin initializes the plugin.
func (p *VideoPlayerPlugin) InitPlugin(messenger plugin.BinaryMessenger) error {
	p.messenger = messenger
	p.videoPlayers = make(map[int32]*VideoPlayer)

	messenger.SetChannelHandler(channelName+"initialize", func(binaryMessage []byte, responseSender plugin.ResponseSender) (err error) {
		return nil
	})
	p.handle("initialize", func(_ interface{}) (interface{}, error) { return nil, nil })
	p.handle("create", p.create)
	p.handle("play", p.play)
	p.handle("pause", p.pause)
	p.handle("position", p.position)
	p.handle("setLooping", p.setLooping)
	p.handle("dispose", p.dispose)
	return nil
}

func (p *VideoPlayerPlugin) handle(method string, handler func(interface{}) (interface{}, error)) {
	p.messenger.SetChannelHandler(channelName+method, func(binaryMessage []byte, responseSender plugin.ResponseSender) (err error) {
		input, err := plugin.StandardMessageCodec{}.DecodeMessage(binaryMessage)
		if err != nil {
			p.sendErrorReply(responseSender, err)
			return
		}
		result, err := handler(input)
		if err != nil {
			p.sendErrorReply(responseSender, err)
			return
		}
		p.sendReply(responseSender, result)
		return
	})
}

func (p *VideoPlayerPlugin) sendReply(responseSender plugin.ResponseSender, message interface{}) {
	binary, err := plugin.StandardMessageCodec{}.EncodeMessage(map[interface{}]interface{}{
		"result": message,
	})
	if err != nil {
		fmt.Println("error encoding message: " + err.Error())
		return
	}
	responseSender.Send(binary)
}

func (p *VideoPlayerPlugin) sendErrorReply(responseSender plugin.ResponseSender, err error) {
	binary, err := plugin.StandardMessageCodec{}.EncodeMessage(map[interface{}]interface{}{
		"result": map[interface{}]interface{}{
			"message": err.Error(),
			"code":    nil,
			"details": nil,
		},
	})
	if err != nil {
		fmt.Println("error encoding message: " + err.Error())
		return
	}
	responseSender.Send(binary)
}

// InitPluginTexture is used to create and manage backend textures
func (p *VideoPlayerPlugin) InitPluginTexture(registry *flutter.TextureRegistry) error {
	p.textureRegistry = registry
	return nil
}

func (p *VideoPlayerPlugin) create(arguments interface{}) (reply interface{}, err error) {
	args := arguments.(map[interface{}]interface{})
	if v, ok := args["asset"]; ok && v != nil {
		return nil, errors.New("only online video and relative path videos are supported")
	}

	texture := p.textureRegistry.NewTexture()
	player := &VideoPlayer{
		uri:     args["uri"].(string),
		texture: &texture,
	}

	eventChannel := plugin.NewEventChannel(p.messenger, fmt.Sprintf("flutter.io/videoPlayer/videoEvents%d", texture.ID), plugin.StandardMethodCodec{})
	eventChannel.Handle(player)

	p.videoPlayersLock.Lock()
	p.videoPlayers[int32(texture.ID)] = player
	p.videoPlayersLock.Unlock()
	texture.Register(player.TextureHandler)

	return map[interface{}]interface{}{
		"textureId": texture.ID,
	}, nil
}

func (p *VideoPlayerPlugin) play(arguments interface{}) (reply interface{}, err error) {
	args := arguments.(map[interface{}]interface{})
	p.videoPlayersLock.RLock()
	defer p.videoPlayersLock.RUnlock()
	p.videoPlayers[args["textureId"].(int32)].Play()
	return nil, nil
}

func (p *VideoPlayerPlugin) pause(arguments interface{}) (reply interface{}, err error) {
	args := arguments.(map[interface{}]interface{})
	p.videoPlayersLock.RLock()
	defer p.videoPlayersLock.RUnlock()
	p.videoPlayers[args["textureId"].(int32)].Pause()
	return nil, nil
}

func (p *VideoPlayerPlugin) position(arguments interface{}) (reply interface{}, err error) {
	args := arguments.(map[interface{}]interface{})
	p.videoPlayersLock.RLock()
	defer p.videoPlayersLock.RUnlock()
	videoPlayer := p.videoPlayers[args["textureId"].(int32)]
	return map[interface{}]interface{}{
		"position": int64(videoPlayer.CurrentTime() * 1000),
	}, nil
}

func (p *VideoPlayerPlugin) setLooping(arguments interface{}) (reply interface{}, err error) {
	args := arguments.(map[interface{}]interface{})
	p.videoPlayersLock.RLock()
	defer p.videoPlayersLock.RUnlock()
	p.videoPlayers[args["textureId"].(int32)].SetLooping(args["isLooping"].(bool))
	return nil, nil
}

func (p *VideoPlayerPlugin) dispose(arguments interface{}) (reply interface{}, err error) {
	args := arguments.(map[interface{}]interface{})
	p.videoPlayersLock.RLock()
	defer p.videoPlayersLock.RUnlock()
	p.videoPlayers[args["textureId"].(int32)].Dispose()
	return nil, nil
}

// VideoPlayer correspond to one instance of a VideoPlayer with his associated texture
// handler, eventChannel, methodChannel handler
type VideoPlayer struct {
	uri             string
	texture         *flutter.Texture
	eventSink       *plugin.EventSink
	currentTime     float64
	masterPts       float64
	masterTime      time.Time
	vFrames         chan *VideoPlayerVideoFrame
	vFramesLastPts  float64
	vFramesLastTime time.Time
	vPendingFrame   *VideoPlayerVideoFrame
	aFrames         chan *VideoPlayerAudioFrame
	aFramesLastPts  float64
	aFramesLastTime time.Time

	vWidth          int
	vHeight         int
	vInputCtx       *gmf.FmtCtx
	vStream         *gmf.Stream
	vCodecCtx       *gmf.CodecCtx
	vConverterCtx   *gmf.SwsCtx
	vTargetCodecCtx *gmf.CodecCtx
	aStream         *gmf.Stream
	aCodecCtx       *gmf.CodecCtx
	aConverterCtx   *gmf.SwrCtx
	aPlayer         *oto.Player

	isAlive     bool
	isStreaming bool
	isPlaying   bool
	isLooping   bool
}

func (p *VideoPlayer) OnListen(arguments interface{}, sink *plugin.EventSink) { // flutter.EventChannel interface
	p.eventSink = sink
	if err := p.init(); err != nil {
		sink.Error("VideoError", fmt.Sprintf("VideoPlayer: init: %v", err), nil)
		sink.EndOfStream()
		return
	}

	vWidth, vHeight := p.Bounds()
	sink.Success(map[interface{}]interface{}{
		"event":    "initialized",
		"duration": int64(p.Duration() * 1000),
		"width":    int32(vWidth),
		"height":   int32(vHeight),
	})
	sink.EndOfStream() // !not a complete implementation
}
func (p *VideoPlayer) OnCancel(arguments interface{}) {} // flutter.EventChannel interface

func (p *VideoPlayer) CurrentTime() float64 {
	return p.currentTime
}

func (p *VideoPlayer) Duration() float64 {
	return p.vInputCtx.Duration()
}

func (p *VideoPlayer) Bounds() (int, int) {
	return p.vWidth, p.vHeight
}

func (p *VideoPlayer) GetFrameRate() float64 {
	a := p.vStream.GetRFrameRate().AVR()
	return float64(a.Den) / float64(a.Num)
}

func (p *VideoPlayer) init() error {
	if p.vInputCtx != nil {
		return nil
	}
	var err error
	p.isAlive = true
	p.vFrames = make(chan *VideoPlayerVideoFrame, bufferSize)
	p.aFrames = make(chan *VideoPlayerAudioFrame, bufferSize)
	p.vInputCtx, err = gmf.NewInputCtx(p.uri)
	if err != nil {
		return err
	}
	p.vStream, err = p.vInputCtx.GetBestStream(gmf.AVMEDIA_TYPE_VIDEO)
	if err != nil {
		p.vInputCtx.Free()
		return errors.New("No video stream found in " + p.uri)
	}
	if err := p.vStream.CodecCtx().Open(nil); err != nil {
		p.vInputCtx.Free()
		p.vStream.Free()
		return err
	}

	targetCodec, err := gmf.FindEncoder(gmf.AV_CODEC_ID_RAWVIDEO)
	if err != nil {
		p.vInputCtx.Free()
		p.vStream.Free()
		return err
	}
	p.vTargetCodecCtx = gmf.NewCodecCtx(targetCodec)
	p.vTargetCodecCtx.SetTimeBase(gmf.AVR{Num: 1, Den: 1}).
		SetPixFmt(gmf.AV_PIX_FMT_RGBA).
		SetWidth(p.vStream.CodecCtx().Width()).
		SetHeight(p.vStream.CodecCtx().Height())
	if targetCodec.IsExperimental() {
		p.vTargetCodecCtx.SetStrictCompliance(gmf.FF_COMPLIANCE_EXPERIMENTAL)
	}
	if err := p.vTargetCodecCtx.Open(nil); err != nil {
		return err
	}

	// Setup converter from source pix_fmt into AV_PIX_FMT_RGBA
	p.vCodecCtx = p.vStream.CodecCtx()
	p.vWidth, p.vHeight = p.vCodecCtx.Width(), p.vCodecCtx.Height()
	p.vConverterCtx, err = gmf.NewSwsCtx(
		p.vCodecCtx.Width(), p.vCodecCtx.Height(), p.vCodecCtx.PixFmt(),
		p.vCodecCtx.Width(), p.vCodecCtx.Height(), p.vTargetCodecCtx.PixFmt(), gmf.SWS_BICUBIC,
	)
	if err != nil {
		return err
	}

	// Audio setup
	p.aStream, err = p.vInputCtx.GetBestStream(gmf.AVMEDIA_TYPE_AUDIO)
	if err != nil && err.Error() != "stream type 1 not found" {
		return err
	}
	// Don't setup audio if there's no audio stream for video
	if p.aStream == nil {
		return nil
	}
	p.aCodecCtx = p.aStream.CodecCtx()
	if err := p.aCodecCtx.Open(nil); err != nil {
		return err
	}

	p.aConverterCtx, err = gmf.NewSwrCtx([]*gmf.Option{
		{"in_sample_fmt", p.aCodecCtx.SampleFmt()},
		{"in_sample_rate", p.aCodecCtx.SampleRate()},
		{"in_channel_count", p.aCodecCtx.Channels()},
		{"out_sample_fmt", gmf.AV_SAMPLE_FMT_S16},
		{"out_sample_rate", speakerSampleRate},
		{"out_channel_count", speakerChannels},
	}, speakerChannels, gmf.AV_SAMPLE_FMT_S16)
	if err != nil {
		return err
	}

	// Initialize global speaker plugin, if not done yet
	if speakerCtx == nil {
		speakerCtx, err = oto.NewContext(speakerSampleRate, speakerChannels, 2, speakerBufferSize)
		if err != nil {
			return err
		}
	}
	p.aPlayer = speakerCtx.NewPlayer()
	return nil
}

func (p *VideoPlayer) stream() {
	if p.isStreaming {
		return
	}
	p.isStreaming = true
	defer func() {
		if err := recover(); err != nil {
			fmt.Println("go-flutter/plugins/video_player: recover:", err)
			p.Dispose()
		}
	}()

	// Start consuming decoded frames from vFrames & aFrames
	go p.streamVideo()
	go p.streamAudio()

	for {
		if !p.isStreaming {
			break
		}
		for !p.isPlaying {
			time.Sleep(50 * time.Millisecond)
		}
		packet, err := p.vInputCtx.GetNextPacket()
		if err != nil && err != io.EOF {
			if packet != nil {
				packet.Free()
			}
			fmt.Println("go-flutter/plugins/video_player: error getting next packet:", err)
			break
		}
		if packet == nil {
			continue
		}

		// Handle audio packets
		if p.aStream != nil && packet.StreamIndex() == p.aStream.Index() {
			frames, err := p.aCodecCtx.Decode(packet)
			if err != nil {
				panic(fmt.Errorf("go-flutter/plugins/video_player: audio decode error: %v", err))
			}
			packet.Free()
			for _, inFrame := range frames {
				outFrame, err := p.aConverterCtx.Convert(inFrame)
				if err != nil {
					panic(fmt.Errorf("go-flutter/plugins/video_player: audio convert error: %v", err))
				}

				timebase := p.aStream.TimeBase()
				time := float64(timebase.AVR().Num) / float64(timebase.AVR().Den) * float64(inFrame.Pts())
				p.aFrames <- &VideoPlayerAudioFrame{time: time, data: outFrame.GetRawAudioData(0)}
				inFrame.Free()
				outFrame.Free()
			}
			continue
		}

		// Don't process packets not comming from our video stream
		if packet.StreamIndex() != p.vStream.Index() {
			packet.Free()
			continue
		}

		frames, err := p.vCodecCtx.Decode(packet)
		if err != nil {
			panic(fmt.Errorf("go-flutter/plugins/video_player: video decode error: %v", err))
		}
		packet.Free()
		// Decode doesn't treat EAGAIN and EOF as an error
		// it returns empty frames
		if len(frames) == 0 {
			continue
		}
		frames, err = gmf.DefaultRescaler(p.vConverterCtx, frames)
		if err != nil {
			panic(fmt.Errorf("go-flutter/plugins/video_player: video convert error: %v", err))
		}
		packets, err := p.vTargetCodecCtx.Encode(frames, 0)
		if err != nil {
			panic(fmt.Errorf("go-flutter/plugins/video_player: video encode error: %v", err))
		}
		for i := range frames {
			frames[i].Free()
		}

		for _, packet := range packets {
			timebase := p.vStream.TimeBase()
			time := float64(timebase.AVR().Num) / float64(timebase.AVR().Den) * float64(packet.Pts())
			p.vFrames <- &VideoPlayerVideoFrame{time: time, packet: packet}
		}
	}
}

func (p *VideoPlayer) streamVideo() {
	for p.isAlive {
		for !p.isPlaying || len(p.vFrames) == 0 {
			time.Sleep(100 * time.Millisecond)
		}
		p.texture.FrameAvailable()
		time.Sleep(5 * time.Millisecond)
	}
}

func (p *VideoPlayer) streamAudio() {
	if p.aStream == nil {
		return
	}
	for p.isAlive {
		for !p.isPlaying || len(p.aFrames) == 0 {
			time.Sleep(100 * time.Millisecond)
		}
		aFrame := <-p.aFrames
		var diff float64 = math.NaN()
		if p.vFramesLastPts != 0 {
			diff = aFrame.time - p.vFramesLastPts
		}
		if diff < -0.3 {
			// drop frame to catch up
		} else if math.IsNaN(diff) {
			// wait for first video frame to be played
			time.Sleep(100 * time.Millisecond)
		} else {
			if diff > 0.1 {
				// wait a bit before playing frame
				time.Sleep(time.Duration(diff*1000.0) * time.Millisecond)
			}
			p.aPlayer.Write(aFrame.data)
		}
		//fmt.Printf("audio frame diff:%.2f vpts:%.3f apts:%.3f vd:%d ad:%d\n", diff, p.vFramesLastPts, p.aFramesLastPts, len(p.vFrames), len(p.aFrames))
		p.aFramesLastPts = aFrame.time
		p.aFramesLastTime = time.Now()
	}
}

func (p *VideoPlayer) Play() {
	p.masterPts = p.vFramesLastPts
	p.masterTime = time.Now()
	p.vFramesLastPts = 0
	p.aFramesLastPts = 0
	p.isPlaying = true
	if !p.isStreaming {
		go p.stream()
	}
}

func (p *VideoPlayer) Pause() {
	p.isPlaying = false
}

func (p *VideoPlayer) SetLooping(looping bool) {
	p.isLooping = looping
}

func (p *VideoPlayer) resetFrames() {
	vFrames := p.vFrames
	p.vFrames = make(chan *VideoPlayerVideoFrame, bufferSize)
	for len(vFrames) > 0 {
		frame := <-vFrames
		frame.Free()
	}
	close(vFrames)
	aFrames := p.aFrames
	p.aFrames = make(chan *VideoPlayerAudioFrame, bufferSize)
	close(aFrames)
}

func (p *VideoPlayer) Dispose() {
	p.isAlive = false
	p.isStreaming = false
	p.isPlaying = false
	if p.texture != nil {
		p.texture.UnRegister()
		p.texture = nil
	}
	p.resetFrames()

	for i := 0; i < p.vInputCtx.StreamsCnt(); i++ {
		stream, _ := p.vInputCtx.GetStream(i)
		defer stream.CodecCtx().Free()
		defer stream.Free()
	}
	p.vConverterCtx.Free()
	p.vTargetCodecCtx.Free()
	if p.aStream != nil {
		p.aConverterCtx.Free()
		p.aPlayer.Close()
	}
	p.vInputCtx.Free()
}

func (p *VideoPlayer) TextureHandler(width, height int) (bool, *flutter.PixelBuffer) {
	frame := p.vPendingFrame
	if frame == nil {
		if len(p.vFrames) == 0 {
			return false, nil
		}
		var ok bool
		frame, ok = <-p.vFrames
		//fmt.Println("texture handler", ok, frame.time, p.vFramesLastPts)
		if !ok {
			return false, nil
		}
	} else {
		p.vPendingFrame = nil
	}

	vWidth, vHeight := p.Bounds()
	p.currentTime = frame.time
	p.vFramesLastPts = frame.time
	p.vFramesLastTime = time.Now()
	pixbuf := &flutter.PixelBuffer{
		Pix:    frame.Data(),
		Width:  vWidth,
		Height: vHeight,
	}

	// Sync with master time
	timeDiff := (float64(time.Now().Sub(p.masterTime)) / float64(time.Second))
	diff := (frame.time - p.masterPts) - timeDiff
	fmt.Printf("video frame diff:%.2f vpts:%.3f apts:%.3f vd:%d ad:%d timediff:%.2f\n", diff, p.vFramesLastPts, p.aFramesLastPts, len(p.vFrames), len(p.aFrames), timeDiff)
	if diff < -0.3 {
		// Render frame & ask for more to catch up
		if len(p.vFrames) > 0 {
			p.texture.FrameAvailable()
		}
		frame.Free()
		return true, pixbuf
	}
	if diff > 0.1 {
		//time.Sleep(time.Duration(diff*1000) * time.Millisecond)
		// Save frame to be used when it's time
		p.vPendingFrame = frame
		return false, nil
	}

	// Render normally, we're in sync with the clock
	frame.Free()
	return true, pixbuf
}

type VideoPlayerVideoFrame struct {
	// ffmpeg packet
	packet *gmf.Packet
	// in second
	time float64
}

func (f *VideoPlayerVideoFrame) Data() []byte {
	return f.packet.Data()
}

func (f *VideoPlayerVideoFrame) Free() {
	f.packet.Free()
}

type VideoPlayerAudioFrame struct {
	// bytes in pcm little endian 16bit format (for oto to use)
	data []byte
	// in second
	time float64
}
