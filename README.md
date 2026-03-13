# Dither Explorer (Go + Ebitengine)

Basic GUI app for exploring dithering with a split layout:
- Left side: 3 stacked 16:9 screens (Input, Algorithm Preview, Output)
- Right side: controls sidebar with live capture + algorithm parameters

## Live capture mode

This app uses Linux desktop portal screen capture (system picker dialog) and consumes the selected PipeWire stream through GStreamer.

When you click **Start Capture**, your desktop environment should show the default system dialog to choose a window/source.

## Prerequisites (Linux)

- Go 1.23+
- GStreamer with PipeWire plugin (`gst-launch-1.0` + `pipewiresrc`)
- xdg-desktop-portal running in your desktop session

On many distros, ensure at least:
- `xdg-desktop-portal`
- the matching portal backend (for example `xdg-desktop-portal-gnome` or `xdg-desktop-portal-kde`)
- `gstreamer` and `gst-plugin-pipewire` packages

## Run

```bash
go run ./...
```

## Controls

- Start Capture / Stop Capture
- Algorithm selector dropdown: Threshold, Floyd-Steinberg
- Rescale dropdown: Nearest, Bilinear, Bicubic, Lanczos (default)
- Pre-scale slider (applies before dithering)
- Output levels slider (snaps to 2, 4, 8, 16)
- Threshold slider
- Error Diffusion slider (shown for Floyd-Steinberg)

## Notes

- Input is live capture only (no static image loading).
- If capture does not start, check app status text in the sidebar for portal or gstreamer errors.
