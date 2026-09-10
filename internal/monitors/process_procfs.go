package monitors

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// procfs exposes CPU and start-time fields in USER_HZ ticks. USER_HZ is the
// stable 100 Hz userspace ABI value, independent of the kernel's CONFIG_HZ.
const processUserHZ uint64 = 100

type processStat struct {
	comm           string
	startTimeTicks uint64
	cpuSeconds     float64
	rssBytes       int64
	virtBytes      int64
	threads        int64
}

// findProcessesAt performs one complete proc-root enumeration for every
// name. A PID disappearing mid-walk is expected and skipped; failure to
// enumerate the root or read boot time rejects the whole scan. When several
// PIDs match a name the oldest wins, so a supervisor's fresh child does not
// mask the long-running process.
func findProcessesAt(root string, names []string) (map[string]processSelection, error) {
	selections := make(map[string]processSelection, len(names))
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[name] = struct{}{}
		selections[name] = processSelection{}
	}

	bootTime, err := readProcBootTime(filepath.Join(root, "stat"))
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		commRaw, err := os.ReadFile(filepath.Join(dir, "comm"))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(commRaw))
		if _, ok := wanted[name]; !ok || !processIdentityMatches(dir, name) {
			continue
		}

		stat, err := readProcessStat(filepath.Join(dir, "stat"))
		if err != nil || stat.comm != name {
			continue
		}
		info := processInfo{
			PID:            pid,
			StartTimeTicks: stat.startTimeTicks,
			StartTimeUnix:  bootTime + int64(stat.startTimeTicks/processUserHZ),
			CPUSeconds:     stat.cpuSeconds,
			RSSBytes:       stat.rssBytes,
			VirtBytes:      stat.virtBytes,
			Threads:        stat.threads,
		}
		sel := selections[name]
		sel.Eligible++
		if !sel.Found || processInfoOlder(info, sel.Info) {
			sel.Info = info
			sel.Found = true
		}
		selections[name] = sel
	}

	for name, sel := range selections {
		if !sel.Found {
			continue
		}
		readOptionalProcessInfo(root, &sel.Info)
		selections[name] = sel
	}
	return selections, nil
}

func processInfoOlder(candidate, current processInfo) bool {
	if candidate.StartTimeTicks != current.StartTimeTicks {
		return candidate.StartTimeTicks < current.StartTimeTicks
	}
	return candidate.PID < current.PID
}

// processIdentityMatches confirms comm against the executable link or
// argv0, since comm is a 15-byte truncation any process can set.
func processIdentityMatches(procDir, name string) bool {
	if exe, err := os.Readlink(filepath.Join(procDir, "exe")); err == nil {
		exe = strings.TrimSuffix(exe, " (deleted)")
		if filepath.Base(exe) == name {
			return true
		}
	}
	cmdline, err := os.ReadFile(filepath.Join(procDir, "cmdline"))
	if err != nil || len(cmdline) == 0 {
		return false
	}
	argv0 := string(bytes.SplitN(cmdline, []byte{0}, 2)[0])
	return argv0 != "" && filepath.Base(argv0) == name
}

func readProcBootTime(path string) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || fields[0] != "btime" {
			continue
		}
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || value <= 0 {
			return 0, fmt.Errorf("invalid proc boot time")
		}
		return value, nil
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, errors.New("proc boot time not found")
}

// readProcessStat parses /proc/PID/stat. comm may contain spaces and
// parentheses, so fields are taken after the last ')'.
func readProcessStat(path string) (processStat, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return processStat{}, err
	}
	closeBracket := bytes.LastIndexByte(raw, ')')
	openBracket := bytes.IndexByte(raw, '(')
	if openBracket < 0 || closeBracket <= openBracket || closeBracket+1 >= len(raw) {
		return processStat{}, errors.New("invalid proc process stat")
	}
	fields := strings.Fields(string(raw[closeBracket+1:]))
	if len(fields) < 22 {
		return processStat{}, errors.New("short proc process stat")
	}
	utime, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return processStat{}, err
	}
	stime, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return processStat{}, err
	}
	threads, err := strconv.ParseInt(fields[17], 10, 64)
	if err != nil || threads < 0 {
		return processStat{}, errors.New("invalid proc thread count")
	}
	startTime, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || startTime == 0 {
		return processStat{}, errors.New("invalid proc process start time")
	}
	virtBytes, err := strconv.ParseInt(fields[20], 10, 64)
	if err != nil || virtBytes < 0 {
		return processStat{}, errors.New("invalid proc virtual size")
	}
	rssPages, err := strconv.ParseInt(fields[21], 10, 64)
	if err != nil {
		return processStat{}, err
	}
	if rssPages < 0 {
		rssPages = 0
	}
	return processStat{
		comm:           string(raw[openBracket+1 : closeBracket]),
		startTimeTicks: startTime,
		cpuSeconds:     float64(utime)/float64(processUserHZ) + float64(stime)/float64(processUserHZ),
		rssBytes:       rssPages * int64(os.Getpagesize()),
		virtBytes:      virtBytes,
		threads:        threads,
	}, nil
}

func readOptionalProcessInfo(root string, info *processInfo) {
	dir := filepath.Join(root, strconv.Itoa(info.PID))
	readProcessStatus(filepath.Join(dir, "status"), info)
	if entries, err := os.ReadDir(filepath.Join(dir, "fd")); err == nil {
		info.OpenFDs = int64(len(entries))
	}
	if maxFDs, ok := readProcessMaxFDs(filepath.Join(dir, "limits")); ok {
		info.MaxFDs = maxFDs
	}
	if values, ok := readProcessIO(filepath.Join(dir, "io")); ok {
		info.IO = values
		info.IOValid = true
	}
}

func readProcessStatus(path string, info *processInfo) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || value < 0 {
			continue
		}
		switch fields[0] {
		case "VmRSS:":
			if value <= math.MaxInt64/1024 {
				info.RSSBytes = value * 1024
			}
		case "VmSize:":
			if value <= math.MaxInt64/1024 {
				info.VirtBytes = value * 1024
			}
		case "Threads:":
			info.Threads = value
		}
	}
}

// readProcessMaxFDs returns the soft open-files limit; ok with 0 means
// unlimited.
func readProcessMaxFDs(path string) (uint64, bool) {
	file, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[0] != "Max" || fields[1] != "open" || fields[2] != "files" {
			continue
		}
		if fields[3] == "unlimited" {
			return 0, true
		}
		value, err := strconv.ParseUint(fields[3], 10, 64)
		if err != nil || value == math.MaxUint64 {
			return 0, false
		}
		return value, true
	}
	return 0, false
}

// readProcessIO requires all four counters; a partial file is rejected so
// a missing field cannot be published as a zero delta.
func readProcessIO(path string) (processIOValues, bool) {
	file, err := os.Open(path)
	if err != nil {
		return processIOValues{}, false
	}
	defer func() { _ = file.Close() }()

	var values processIOValues
	seen := make(map[string]bool, 4)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		var dst *uint64
		var op string
		switch fields[0] {
		case "read_bytes:":
			dst, op = &values.ReadBytes, "read_bytes"
		case "write_bytes:":
			dst, op = &values.WriteBytes, "write_bytes"
		case "syscr:":
			dst, op = &values.ReadSyscalls, "read_syscalls"
		case "syscw:":
			dst, op = &values.WriteSyscalls, "write_syscalls"
		default:
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return processIOValues{}, false
		}
		*dst = value
		seen[op] = true
	}
	if scanner.Err() != nil {
		return processIOValues{}, false
	}
	for _, op := range processIOOperations {
		if !seen[op] {
			return processIOValues{}, false
		}
	}
	return values, true
}
