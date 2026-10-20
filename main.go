// oitc-ipmi-dump reads the sensors, the SDR repository and the SEL of the local BMC (in-band) and writes them to a
// JSON file. Only read commands are sent to the BMC, all other commands are refused.
//
// The IPMI commands are recorded byte by byte, so the openITCOCKPIT Agent can replay them in its unit tests.
// For the recording the program calls itself through the "tool" interface of go-ipmi ("oitc-ipmi-dump raw ...").
package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/bougou/go-ipmi/pkg/client"
	"github.com/bougou/go-ipmi/pkg/types"
)

const version = "1.2"

const logEnv = "OITC_IPMI_DUMP_LOG"

// Read only commands (NetFn, command)
var allowedCommands = map[[2]uint8]string{
	{0x00, 0x01}: "Get Chassis Status",
	{0x04, 0x20}: "Get Device SDR Info",
	{0x04, 0x21}: "Get Device SDR",
	{0x04, 0x22}: "Reserve Device SDR Repository",
	{0x04, 0x23}: "Get Sensor Reading Factors",
	{0x04, 0x25}: "Get Sensor Hysteresis",
	{0x04, 0x27}: "Get Sensor Thresholds",
	{0x04, 0x29}: "Get Sensor Event Enable",
	{0x04, 0x2b}: "Get Sensor Event Status",
	{0x04, 0x2d}: "Get Sensor Reading",
	{0x04, 0x2f}: "Get Sensor Type",
	{0x06, 0x01}: "Get Device ID",
	{0x0a, 0x20}: "Get SDR Repository Info",
	{0x0a, 0x21}: "Get SDR Repository Allocation Info",
	{0x0a, 0x22}: "Reserve SDR Repository",
	{0x0a, 0x23}: "Get SDR",
	{0x0a, 0x40}: "Get SEL Info",
	{0x0a, 0x41}: "Get SEL Allocation Info",
	{0x0a, 0x42}: "Reserve SEL",
	{0x0a, 0x43}: "Get SEL Entry",
	{0x0a, 0x48}: "Get SEL Time",
}

type exchange struct {
	NetFn    string `json:"netfn"`
	Cmd      string `json:"cmd"`
	Name     string `json:"name"`
	Request  string `json:"request"`
	Code     string `json:"completion_code"`
	Response string `json:"response"`
	Error    string `json:"error,omitempty"`
}

type sensor struct {
	Number      string            `json:"number"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Entity      string            `json:"entity"`
	Threshold   bool              `json:"threshold"`
	Reading     string            `json:"reading"`
	Unit        string            `json:"unit"`
	Status      string            `json:"status"`
	Valid       bool              `json:"reading_valid"`
	Scanning    bool              `json:"scanning_disabled"`
	NotPresent  bool              `json:"not_present"`
	Active      []uint8           `json:"active_states,omitempty"`
	Human       string            `json:"human"`
	Thresholds  map[string]string `json:"thresholds,omitempty"`
	SDRType     string            `json:"sdr_type"`
	GeneratorID string            `json:"generator_id"`
}

type result struct {
	Errors      []string `json:"errors"`
	DeviceID    any      `json:"device_id,omitempty"`
	Chassis     any      `json:"chassis_status,omitempty"`
	Sensors     []sensor `json:"sensors,omitempty"`
	SELInfo     any      `json:"sel_info,omitempty"`
	SEL         string   `json:"sel,omitempty"`
	DurationSec float64  `json:"duration_seconds"`
}

type dump struct {
	Tool      string     `json:"tool"`
	Version   string     `json:"version"`
	Created   string     `json:"created"`
	OS        string     `json:"os"`
	Direct    result     `json:"direct"`
	Recorded  result     `json:"recorded"`
	Exchanges []exchange `json:"exchanges"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "raw" {
		os.Exit(proxy(os.Args[2:]))
	}

	output := flag.String("o", "ipmi-dump.json", "output file")
	timeout := flag.Duration("timeout", 5*time.Minute, "timeout")
	showVersion := flag.Bool("version", false, "print the version")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "oitc-ipmi-dump %s - reads the local BMC (read only) and writes the result to a JSON file\n\nUsage: %s [options]\n\n", version, os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	d := dump{Tool: "oitc-ipmi-dump", Version: version, Created: time.Now().Format(time.RFC3339), OS: runtime.GOOS + "/" + runtime.GOARCH, Exchanges: []exchange{}}

	fmt.Println("Reading the BMC directly ...")
	direct, err := client.NewOpenClient()
	if err == nil {
		d.Direct = collect(ctx, direct)
	} else {
		d.Direct.Errors = append(d.Direct.Errors, err.Error())
	}
	if d.Direct.DeviceID == nil {
		// Not even the device id could be read, e.g. a system without BMC: the recording would fail as well
		writeDump(&d, *output)
		fmt.Printf("No BMC found, see the errors in %s\n", *output)
		return
	}

	fmt.Println("Recording the IPMI commands ...")
	exe, err := os.Executable()
	logFile, err2 := os.CreateTemp("", "oitc-ipmi-dump-*.jsonl")
	if err != nil || err2 != nil {
		d.Recorded.Errors = append(d.Recorded.Errors, fmt.Sprintf("%v %v", err, err2))
	} else {
		logFile.Close()
		defer os.Remove(logFile.Name())
		os.Setenv(logEnv, logFile.Name())
		recorder, _ := client.NewToolClient(exe)
		d.Recorded = collect(ctx, recorder)
		d.Exchanges = readExchanges(logFile.Name())
	}

	writeDump(&d, *output)
	fmt.Printf("%d sensors, %d IPMI commands, errors: %d/%d\nWritten to %s\n",
		len(d.Direct.Sensors), len(d.Exchanges), len(d.Direct.Errors), len(d.Recorded.Errors), *output)
}

func writeDump(d *dump, output string) {
	data, _ := json.MarshalIndent(d, "", "  ")
	if err := os.WriteFile(output, data, 0600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func collect(ctx context.Context, c *client.Client) result {
	r := result{Errors: []string{}}
	start := time.Now()
	addErr := func(what string, err error) {
		r.Errors = append(r.Errors, fmt.Sprintf("%s: %v", what, err))
	}
	if err := c.Connect(ctx); err != nil {
		addErr("connect", err)
		return r
	}
	defer c.Close(ctx)

	if res, err := c.GetDeviceID(ctx); err == nil {
		r.DeviceID = res
	} else {
		addErr("device id", err)
	}
	if res, err := c.GetChassisStatus(ctx); err == nil {
		r.Chassis = res
	} else {
		addErr("chassis status", err)
	}
	if sensors, err := c.GetSensors(ctx); err == nil {
		for _, s := range sensors {
			entry := sensor{
				Number: fmt.Sprintf("%#02x", s.Number), Name: s.Name, Type: s.SensorType.String(), Entity: s.EntityID.String(),
				Threshold: s.IsThreshold(), Reading: s.ReadingStr(), Unit: s.SensorUnit.String(), Status: s.Status(),
				Valid: s.IsReadingValid(), Scanning: s.ScanningDisabled, NotPresent: s.NotPresent, Human: s.HumanStr(),
				SDRType: s.SDRRecordType.String(), GeneratorID: fmt.Sprintf("%#04x", uint16(s.GeneratorID)),
			}
			if s.IsThreshold() {
				entry.Thresholds = map[string]string{}
				for _, t := range []types.SensorThresholdType{types.SensorThresholdType_LNR, types.SensorThresholdType_LCR, types.SensorThresholdType_LNC,
					types.SensorThresholdType_UNC, types.SensorThresholdType_UCR, types.SensorThresholdType_UNR} {
					entry.Thresholds[t.Abbr()] = s.ThresholdStr(t)
				}
			} else {
				entry.Active = s.DiscreteActiveEvents()
			}
			r.Sensors = append(r.Sensors, entry)
		}
	} else {
		addErr("sensors", err)
	}
	selEntries := -1
	if res, err := c.GetSELInfo(ctx); err == nil {
		r.SELInfo = res
		selEntries = int(res.Entries)
	} else {
		addErr("sel info", err)
	}
	// An empty SEL answers Get SEL Entry with 0xcb (record not found)
	if selEntries != 0 {
		if entries, err := c.GetSELEntries(ctx, 0); err == nil {
			r.SEL = types.FormatSELs(entries, nil)
		} else {
			addErr("sel entries", err)
		}
	}
	r.DurationSec = time.Since(start).Seconds()
	return r
}

// proxy executes a single command like "ipmitool raw 0x06 0x01" and records it
func proxy(args []string) int {
	msg := make([]byte, 0, len(args))
	for _, arg := range args {
		value, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(arg), "0x"), 16, 8)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid byte %s\n", arg)
			return 1
		}
		msg = append(msg, uint8(value))
	}
	if len(msg) < 2 {
		fmt.Fprintln(os.Stderr, "netfn and cmd required")
		return 1
	}
	netfn, cmd, data := msg[0], msg[1], msg[2:]
	ex := exchange{NetFn: fmt.Sprintf("%#02x", netfn), Cmd: fmt.Sprintf("%#02x", cmd), Request: hex.EncodeToString(data)}
	defer appendExchange(&ex)

	name, ok := allowedCommands[[2]uint8{netfn, cmd}]
	if !ok {
		// Not a read command: refuse it (Invalid Command)
		ex.Name = "refused"
		ex.Code = "0xc1"
		fmt.Fprintf(os.Stderr, "Unable to send RAW command (channel=0x0 netfn=0x%x lun=0x0 cmd=0x%x rsp=0xc1): Invalid command\n", netfn, cmd)
		return 1
	}
	ex.Name = name

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := client.NewOpenClient()
	if err == nil {
		err = c.Connect(ctx)
	}
	if err != nil {
		ex.Error = err.Error()
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer c.Close(ctx)

	res, err := c.RawCommand(ctx, types.NetFn(netfn), cmd, data, name)
	if err != nil {
		ex.Error = err.Error()
		ex.Code = "" // No answer of the BMC, e.g. a timeout
		var respErr *types.ResponseError
		if errors.As(err, &respErr) {
			ex.Code = fmt.Sprintf("%#02x", uint8(respErr.CompletionCode()))
			fmt.Fprintf(os.Stderr, "Unable to send RAW command (channel=0x0 netfn=0x%x lun=0x0 cmd=0x%x rsp=0x%02x): %s\n", netfn, cmd, uint8(respErr.CompletionCode()), err)
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
		return 1
	}
	ex.Code = "0x00"
	ex.Response = hex.EncodeToString(res.Response)
	parts := make([]string, len(res.Response))
	for i, b := range res.Response {
		parts[i] = fmt.Sprintf("%02x", b)
	}
	fmt.Println(strings.Join(parts, " "))
	return 0
}

func appendExchange(ex *exchange) {
	path := os.Getenv(logEnv)
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	line, _ := json.Marshal(ex)
	f.Write(append(line, '\n'))
}

func readExchanges(path string) []exchange {
	result := []exchange{}
	f, err := os.Open(path)
	if err != nil {
		return result
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var ex exchange
		if json.Unmarshal(scanner.Bytes(), &ex) == nil {
			result = append(result, ex)
		}
	}
	return result
}
