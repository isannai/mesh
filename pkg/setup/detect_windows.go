//go:build windows

package setup

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func detectMemory() MemInfo {
	return detectMemoryWindows()
}

func detectMemoryWindows() MemInfo {
	type memoryStatusEx struct {
		Length               uint32
		MemoryLoad           uint32
		TotalPhys            uint64
		AvailPhys            uint64
		TotalPageFile        uint64
		AvailPageFile        uint64
		TotalVirtual         uint64
		AvailVirtual         uint64
		AvailExtendedVirtual uint64
	}
	var mem memoryStatusEx
	mem.Length = uint32(unsafe.Sizeof(mem))
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	proc := kernel32.NewProc("GlobalMemoryStatusEx")
	proc.Call(uintptr(unsafe.Pointer(&mem)))
	return MemInfo{
		TotalGB: roundGB(mem.TotalPhys),
	}
}

func detectDisk(dir string) DiskInfo {
	info := DiskInfo{ModelDir: dir}
	if dir == "" {
		return info
	}
	p, _ := windows.UTF16PtrFromString(dir)
	var free, total, totalFree uint64
	windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree)
	info.FreeGB = roundGB(free)
	return info
}

// FreeDiskBytes returns the number of bytes available to the current user on
// the volume containing dir. Used by the installer for pre-download capacity
// check — detectDisk's roundGB loses precision (a few-MB shortfall would
// silently round to 0 GB difference), so this returns raw bytes.
//
// Returns an error when dir is empty, malformed, or unreachable; callers
// typically log + skip the check rather than aborting (rare to fail).
func FreeDiskBytes(dir string) (uint64, error) {
	if dir == "" {
		return 0, fmt.Errorf("FreeDiskBytes: empty path")
	}
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, fmt.Errorf("UTF16: %w", err)
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return 0, fmt.Errorf("GetDiskFreeSpaceEx %s: %w", dir, err)
	}
	return free, nil
}

// hasCUDA12Runtime checks if cudart64_12.dll is available on the system.
func hasCUDA12Runtime() bool {
	dll := windows.NewLazySystemDLL("cudart64_12.dll")
	return dll.Load() == nil
}

// fetchCPUProcessorID collects all CPU ProcessorIds, sorts and joins them.
func fetchCPUProcessorID() string {
	out, err := exec.Command("wmic", "cpu", "get", "ProcessorId", "/value").Output()
	if err != nil {
		return "unknown-cpu"
	}
	var ids []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ProcessorId=") {
			id := strings.TrimSpace(strings.TrimPrefix(line, "ProcessorId="))
			if id != "" {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return "unknown-cpu"
	}
	sort.Strings(ids)
	return strings.Join(ids, ":")
}

// fetchGPUUUID collects all GPU UUIDs via nvidia-smi, sorts and joins them.
func fetchGPUUUID() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=gpu_uuid", "--format=csv,noheader").Output()
	if err != nil {
		return "no-gpu"
	}
	var uuids []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			uuids = append(uuids, line)
		}
	}
	if len(uuids) == 0 {
		return "no-gpu"
	}
	sort.Strings(uuids)
	return strings.Join(uuids, ":")
}

// fetchMainboardUUID returns the system UUID (SMBIOS), or "" when it cannot be
// read. "" is a hard failure for the caller — see DeriveNodeIdentity.
//
// 🔴 CIM FIRST, wmic ONLY AS A FALLBACK. wmic is being removed from Windows
// (deprecated since 21H1, absent on 24H2+ installs). When it went away this
// function returned "" with no error and the node kept going, deriving its
// address from an EMPTY uuid — a different address. Two dev nodes changed
// identity on the same day that way, and everything keyed to the old address
// (the RV's TPM key binding, its prober roster, the name on chain) silently
// stopped matching. Nothing on the machines had changed but a Windows update.
// See workspace docs/issues.md WS-05.
//
// 🔴 KEEP THIS IDENTICAL TO glink pkg/setup. station and probe derive the same
// address isannd does, and isannd re-signs the register frames station sends.
// If the two disagree the RV's signature check fails and the node stops
// registering at all — worse than the drift this fixes.
//
// PowerShell costs a few hundred ms and this runs once per process (callers
// cache the identity), so the order is correctness first, speed second.
func fetchMainboardUUID() string {
	if uuid := mainboardUUIDFromCIM(); uuid != "" {
		return uuid
	}
	return mainboardUUIDFromWMIC()
}

// mainboardUUIDFromCIM asks WMI through PowerShell, which every supported
// Windows still ships.
func mainboardUUIDFromCIM() string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive",
		"-Command", "(Get-CimInstance -ClassName Win32_ComputerSystemProduct).UUID").Output()
	if err != nil {
		return ""
	}
	return validMainboardUUID(string(out))
}

// mainboardUUIDFromWMIC is the legacy path — kept for builds that still ship
// wmic and for machines where PowerShell is locked down.
func mainboardUUIDFromWMIC() string {
	out, err := exec.Command("wmic", "csproduct", "get", "UUID", "/value").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "UUID=") {
			if uuid := validMainboardUUID(strings.TrimPrefix(line, "UUID=")); uuid != "" {
				return uuid
			}
		}
	}
	return ""
}

// validMainboardUUID trims a raw reading and rejects the placeholders firmware
// hands out when it has nothing real. An all-zero or all-F uuid is shared by
// every machine with that firmware, so accepting one would collapse their
// identities into a single address.
func validMainboardUUID(raw string) string {
	uuid := strings.TrimSpace(raw)
	switch strings.ToUpper(uuid) {
	case "", "FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF", "00000000-0000-0000-0000-000000000000",
		"NOT SETTABLE", "NOT PRESENT", "NOT AVAILABLE":
		return ""
	}
	return uuid
}

// mainboardUUIDHint tells the operator what to check when the uuid cannot be
// read. Named per platform because the causes have nothing in common.
func mainboardUUIDHint() string {
	return " — `(Get-CimInstance Win32_ComputerSystemProduct).UUID` must answer;" +
		" wmic is absent on Windows 24H2+ and is only a fallback here"
}

// detectRAMFreeGB returns available physical memory in GB using GlobalMemoryStatusEx.
func detectRAMFreeGB() float64 {
	type memoryStatusEx struct {
		Length               uint32
		MemoryLoad           uint32
		TotalPhys            uint64
		AvailPhys            uint64
		TotalPageFile        uint64
		AvailPageFile        uint64
		TotalVirtual         uint64
		AvailVirtual         uint64
		AvailExtendedVirtual uint64
	}
	var mem memoryStatusEx
	mem.Length = uint32(unsafe.Sizeof(mem))
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	proc := kernel32.NewProc("GlobalMemoryStatusEx")
	proc.Call(uintptr(unsafe.Pointer(&mem)))
	return roundGB(mem.AvailPhys)
}

// detectCPUsDynamic returns per-CPU dynamic metrics (temperature) via WMI thermal zones.
// Returns CPU name with temp; returns temp=-1 on failure.
func detectCPUsDynamic() []CPUSpec {
	ctx2s, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	name := ""
	out, err := exec.CommandContext(ctx2s, "wmic", "cpu", "get", "Name", "/value").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Name=") {
				name = strings.TrimSpace(strings.TrimPrefix(line, "Name="))
				break
			}
		}
	}

	// Try to get CPU temperature via WMI MSAcpi_ThermalZoneTemperature
	// Temperature is in tenths of Kelvin: (value - 2731) / 10 = Celsius
	tempC := -1
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	out2, err2 := exec.CommandContext(ctx2, "wmic", "/namespace:\\\\root\\wmi",
		"PATH", "MSAcpi_ThermalZoneTemperature", "get", "CurrentTemperature", "/value").Output()
	if err2 == nil {
		for _, line := range strings.Split(string(out2), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "CurrentTemperature=") {
				val := strings.TrimPrefix(line, "CurrentTemperature=")
				if v, err := strconv.Atoi(strings.TrimSpace(val)); err == nil && v > 0 {
					tempC = (v - 2731) / 10
					break
				}
			}
		}
	}

	return []CPUSpec{{Name: name, TempC: tempC}}
}

func detectCPUClock() float64 {
	out, err := exec.Command("wmic", "cpu", "get", "MaxClockSpeed", "/value").Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "MaxClockSpeed=") {
			val := strings.TrimPrefix(line, "MaxClockSpeed=")
			mhz, _ := strconv.ParseFloat(strings.TrimSpace(val), 64)
			return mhz
		}
	}
	return 0
}
