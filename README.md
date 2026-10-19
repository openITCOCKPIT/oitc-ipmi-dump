# oitc-ipmi-dump

`oitc-ipmi-dump` reads the sensors, the sensor data records (SDR) and the system event log (SEL) of the local BMC
of a server through IPMI and writes them into a JSON file. Every IPMI command and the answer of the BMC are recorded
byte by byte.

The [openITCOCKPIT Monitoring Agent](https://github.com/openITCOCKPIT/openitcockpit-agent-go) uses these recordings
to test its IPMI check: the agent replays them in its unit tests, so the check gets tested against the BMCs of many
vendors and server generations without access to the hardware.

**Please help us by sending a dump of your servers!** Dumps of different vendors (Dell iDRAC, HPE iLO, Lenovo XClarity,
Supermicro, Fujitsu iRMC, Intel, Gigabyte, ASRock Rack, ...) and of servers with hardware problems (failed power supply,
missing redundancy, failed fan, degraded RAID, memory errors in the SEL) are especially welcome.

## Safety: read only

The tool only sends read commands to the BMC. Every other command is refused before it reaches the BMC, so it can not
change the configuration, power off the server or clear the SEL. The allowed commands are listed in `allowedCommands`
in [main.go](main.go):

| Command | NetFn |
|---|---|
| Get Device ID | App |
| Get Chassis Status | Chassis |
| Get SDR Repository Info, Get SDR Repository Allocation Info, Reserve SDR Repository, Get SDR | Storage |
| Get SEL Info, Get SEL Allocation Info, Reserve SEL, Get SEL Entry, Get SEL Time | Storage |
| Get Device SDR Info, Get Device SDR, Reserve Device SDR Repository | Sensor/Event |
| Get Sensor Reading, Get Sensor Thresholds, Get Sensor Hysteresis, Get Sensor Event Enable, Get Sensor Event Status, Get Sensor Reading Factors, Get Sensor Type | Sensor/Event |

A reservation only protects the following reads of the SDR repository or the SEL, it does not change any data.

## What is in a dump

- The device id of the BMC (manufacturer, product id, firmware version, IPMI version)
- The chassis status (power state, faults)
- All sensors: names, types, readings, thresholds and states
- The system event log (SEL): time, sensor and description of the events
- The time of the dump and the operating system (e.g. `linux/amd64`)

**Not** in a dump: host name, IP or MAC addresses, serial numbers and other FRU data, users and passwords of the BMC,
network configuration of the BMC.


> [!WARNING]
> Please have a look at the JSON file before you send it. The SEL shows when events like power supply failures happened.

## Usage

Download the binary for your system from the [releases](../../releases) or build it yourself (see below).
The tool is a single static binary without dependencies, `ipmitool` is not required.

### Linux

The tool requires root and the IPMI kernel modules (`/dev/ipmi0`). They are loaded automatically on most servers,
otherwise load them with `modprobe ipmi_si ipmi_devintf`.

```
chmod +x oitc-ipmi-dump-1.1-linux-amd64
sudo ./oitc-ipmi-dump-1.1-linux-amd64 -o ipmi-dump.json
```

### Windows

The tool uses the Microsoft IPMI driver (WMI class `Microsoft_IPMI`), which is available on Windows Server if the
system has a BMC. Run it in a command prompt as administrator:

```
oitc-ipmi-dump-1.1-windows-amd64.exe -o ipmi-dump.json
```

### Output

```
Reading the BMC directly ...
Recording the IPMI commands ...
56 sensors, 340 IPMI commands, errors: 0/0
Written to ipmi-dump.json
```

The tool reads the BMC twice: once directly and once through a recording proxy (the tool calls itself for every
command, `oitc-ipmi-dump raw ...`). Depending on the BMC this takes a few seconds up to a minute.
On a system without BMC the tool stops after the first error and writes the error into the file.

Options:

```
-o string         output file (default "ipmi-dump.json")
-timeout duration timeout (default 5m0s)
-version          print the version
```

## Sending a dump

Please open an issue in this repository and attach the JSON file. Please tell us

- the vendor and model of the server (e.g. Dell PowerEdge R650)
- whether we may publish the dump as test data in the repository of the openITCOCKPIT Monitoring Agent.
  Dumps without this permission are only used for tests which are not published.
- anything special about the server, e.g. a power supply which is unplugged on purpose

In case you have any question or want to get in touch, please join the
[openITCOCKPIT Community](https://openitcockpit.io/community/)

## Building

Requires Go 1.24 or newer.

```
go test ./...
./build.sh
```

`build.sh` creates static binaries for Linux (amd64, arm64) and Windows (amd64), the licenses of the libraries which are
compiled into the binaries (`THIRD_PARTY_LICENSES.txt`) and the SHA256 checksums in `dist/`.

## Format of the dump

```json
{
  "tool": "oitc-ipmi-dump",
  "version": "1.1",
  "created": "2026-10-16T08:56:50+02:00",
  "os": "linux/amd64",
  "direct":   { "errors": [], "device_id": {}, "chassis_status": {}, "sensors": [], "sel_info": {}, "sel": "", "duration_seconds": 1.7 },
  "recorded": { "errors": [], "...": "the same as direct, read through the recording proxy" },
  "exchanges": [
    { "netfn": "0x04", "cmd": "0x2d", "name": "Get Sensor Reading", "request": "01", "completion_code": "0x00", "response": "2fc0c000" }
  ]
}
```

`request` and `response` are the hex encoded data of the IPMI messages (without NetFn, command and completion code).
`completion_code` is empty if the BMC did not answer, e.g. on a timeout.

`direct` and `recorded` contain the sensors as interpreted by [go-ipmi](https://github.com/bougou/go-ipmi), the IPMI
library which is used by the tool and the agent. The readings can differ slightly between both, as the sensors are
read twice.

Limitation: the recording proxy does not know the IPMB address of a command, so sensors of other controllers (e.g. the
Intel Management Engine) are recorded as if they belonged to the BMC. The `direct` part contains the correct values.

## License

Copyright (c) 2026 AVENDIS GmbH, MIT License, see [LICENSE](LICENSE).

The binaries contain third-party libraries, most notably [go-ipmi](https://github.com/bougou/go-ipmi)
(Apache License 2.0). Their licenses are part of every release (`THIRD_PARTY_LICENSES.txt`).
