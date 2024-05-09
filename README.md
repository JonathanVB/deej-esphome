# deej-esphome

Deej-esphome is a fork of the [Deej project](https://github.com/omriharel/deej) build to query a ESPHome device through it's REST API over WiFi. This allows the device to also control smart home devices, as well as the computer volume, potentially across multiple systems simultaneously using the same controller (or even the same slider).

In theory, any sensor can be used to control the volume, allowing for some useful, and some silly automation.

This could also be trivally extended to make use of any JSON REST API, not just ESPHome's.

I've cut down this README to items relevant only to this fork, and would encourage you to read [original README](https://github.com/omriharel/deej) first.

## How it works

### Software

- The code running on the ESPHome board is a standard ESPHome firmware with custom sensors ([example configuration](./arduino/esphome-sample.yaml)). These are constantly queried by the PC client through it's built-in REST API.
- The PC runs a lightweight [Go client](./pkg/deej/cmd/main.go) in the background. This client connects to the ESPHome device by IP address, and makes simple HTTP GET commands to determine the sensor statusus.

## Slider mapping (configuration)

deej uses a simple YAML-formatted configuration file named [`config.yaml`](./config.yaml), placed alongside the deej executable.

The config file determines which applications (and devices) are mapped to which sliders, and which parameters to use for the connection to the ESPHome board, as well as other user preferences.

The ESPHome IP, and sensor names should also be included

**This file auto-reloads when its contents are changed, so you can change application mappings on-the-fly without restarting deej, though a restart will be needed if the device is changed between serial and ESPHome devices.**

It looks like this:

```yaml
slider_mapping:
  0: master
  1: chrome.exe
  2: spotify.exe
  3:
    - pathofexile_x64.exe
    - rocketleague.exe
  4: discord.exe

# set this to true if you want the controls inverted (i.e. top is 0%, bottom is 100%)
invert_sliders: false

# use esphome instead of serial
use_esphome: true

esphome_ip: 192.168.1.193

esphome_mapping:
  - input_0
  - input_1

# settings for connecting to the arduino board
com_port: COM4
baud_rate: 9600

# adjust the amount of signal noise reduction depending on your hardware quality
# supported values are "low" (excellent hardware), "default" (regular hardware) or "high" (bad, noisy hardware)
noise_reduction: default
```

## Extensions
This was coded as a weekend project by someone with no prior experience in Go, so there are bound to be quality issues. Beyond that, there's oppertunity for the following:
- Make use of the proper ESPHome libraries that establish a API connection to the device. I attempted to get this working, but gave up after sinking too much time into it.
- Extend to configue any REST API, not just ESPHome. This should just require a relavily trivial URL and config change, and changing the JSON parsing to be more configurable.

## License

deej is released under the [MIT license](./LICENSE).
