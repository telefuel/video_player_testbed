import 'dart:io';
import 'package:path/path.dart';
import 'package:video_player/video_player.dart';
import 'package:flutter/material.dart';

void main() {
  runApp(App());
}

class App extends StatefulWidget {
  App({Key key}) : super(key: key);

  @override
  _AppState createState() => _AppState();
}

class _AppState extends State<App> {
  VideoPlayerController _controller;

  @override
  void initState() {
    super.initState();
    _controller = VideoPlayerController.file(
        File(dirname(Platform.resolvedExecutable) + '/../../../../assets/bbb.mp4'))
      ..initialize().then((_) {
        setState(() {});
      });
  }

  @override
  void dispose() {
    super.dispose();
    _controller.dispose();
  }

  void _togglePlaying() {
    setState(() {
      _controller.value.isPlaying
                  ? _controller.pause()
                  : _controller.play();
    });
  }

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Video Player Testbed',
      theme: ThemeData(
        primarySwatch: Colors.blue,
        visualDensity: VisualDensity.adaptivePlatformDensity,
      ),
      home: Scaffold(
        appBar: AppBar(
          title: Text('Video Player Testbed'),
        ),
        body: Center(
          child: _controller.value.initialized
                ? AspectRatio(
                    aspectRatio: _controller.value.aspectRatio,
                    child: VideoPlayer(_controller),
                  )
                : Container()
        ),
        floatingActionButton: FloatingActionButton(
          onPressed: _togglePlaying,
          tooltip: 'Play/Pause',
          child: Icon(_controller.value.isPlaying ? Icons.pause : Icons.play_arrow),
        ),
      ),
    );
  }
}
