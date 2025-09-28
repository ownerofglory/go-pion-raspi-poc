# Go + Pion (WebRTC) on Raspberry Pi PoC

It's a PoC [WebRTC](https://webrtc.org/) client for [Raspberry Pi](https://www.raspberrypi.com/), built with [Pion](https://pion.ly/) ([GitHub Repo](https://github.com/pion/webrtc))
and [GStreamer](https://gstreamer.freedesktop.org/).
It connects to a signaling server, streams camera/microphone over WebRTC, and supports data channels for testing peer connections


## Prerequisites

I tested it on a **Raspberry Pi model 4B** (1GB RAM, 1,4 GHz ARM Cortex-A53 Quad-Core-CPU) but
I guess it can easily run on a smaller model like **Raspberry Pi Zero** or **Raspberry Pi Zero 2 W**

- `Gstreamer` installed on Raspberry Pi
- (Optional) `Go` installed, otherwise cross-compile the program for raspi
- Camera [and microphone if needed], connected to raspi
- Signaling server or any other mean to exchange the SPD messages and ICE-candidates

I used my own ["playground" signaling server](https://github.com/ownerofglory/webrtc-signaling-go) on websockets


## Build and run

### [Cross-]compile for Raspi
```shell
make
```
### Drop the program into your raspi

```shell
scp ./build/pi-client-arm64 <user>@<raspberry_host>:~
```

### Run on Raspi