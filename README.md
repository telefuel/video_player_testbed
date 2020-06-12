# video_player_testbed

Telefuel wants to use [go-flutter-desktop](https://github.com/go-flutter-desktop/go-flutter/)
in order to build a cross platform desktop app.

Sadly the [`video_player`](https://github.com/flutter/plugins/tree/master/packages/video_player)
official Flutter plugin's native bindings don't exist yet for `go-flutter-desktop`
(see plugins they have bindings for [here](https://github.com/go-flutter-desktop/plugins)).

Now somebody did start working on it, but the current upstreamed version only supports
rendering video, no audio, and simply approximates speed using the framerate. There are also
memory leaks.

**Our goals is to get this video player plugin to support the same interface as
the upstream video_player plugin and work on linux, windows and mac (with the
help of ffmpeg, but open to using something else that can easily be embedded)**

The current video_player implementation can be found in [`go/cmd/video_player.go`](https://github.com/telefuel/video_player_testbed/blob/master/go/cmd/video_player.go). 
It does support audio decoding and playing but syncing needs work to make
playback work smoothly.

An interesting tutorial talking about writing an ffmpeg based media player 
can be found here: http://dranger.com/ffmpeg/tutorial05.html

And other ressource would be the `ffplay` example ffmpeg based player: https://git.ffmpeg.org/gitweb/ffmpeg.git/blob/HEAD:/fftools/ffplay.c

## Dependencies
* `go` 1.13 or higher
* `flutter` latest
* `hover` latest. Install: `GO111MODULE=on go get -u -a github.com/go-flutter-desktop/hover`

## Development
For development, enter `make run` to launch a `go-flutter` desktop window. Press play to start playing the video. When making code changes, you'll have to start the process over.
