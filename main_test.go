package main

import "testing"

// Only read commands may be sent to the BMC
func TestAllowedCommandsAreReadOnly(t *testing.T) {
	for command, name := range allowedCommands {
		if len(name) < 4 || (name[:4] != "Get " && name[:8] != "Reserve ") {
			t.Errorf("command %#02x %#02x (%s) is not a read command", command[0], command[1], name)
		}
	}
	// A few write commands which must never be allowed
	for _, command := range [][2]uint8{
		{0x00, 0x02}, // Chassis Control (power off, reset, ...)
		{0x0a, 0x44}, // Add SEL Entry
		{0x0a, 0x47}, // Clear SEL
		{0x0a, 0x24}, // Add SDR
		{0x0a, 0x27}, // Clear SDR Repository
		{0x04, 0x26}, // Set Sensor Thresholds
		{0x06, 0x02}, // Cold Reset
		{0x06, 0x45}, // Set User Name
		{0x0c, 0x01}, // Set LAN Configuration Parameters
	} {
		if _, ok := allowedCommands[command]; ok {
			t.Errorf("write command %#02x %#02x is allowed", command[0], command[1])
		}
	}
}

// Commands which are not read commands are refused without access to the BMC
func TestProxyRefusesWriteCommands(t *testing.T) {
	t.Setenv(logEnv, "")
	if code := proxy([]string{"0x0a", "0x47", "0x00", "0x00"}); code == 0 {
		t.Error("Clear SEL was not refused")
	}
	if code := proxy([]string{"0x00"}); code == 0 {
		t.Error("an incomplete command was not refused")
	}
}
