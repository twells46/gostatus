package main

// #cgo LDFLAGS: -lX11
// #include <X11/Xlib.h>
// #include <stdlib.h>
//Window def_root_win(Display *dpy) {
//	return DefaultRootWindow(dpy);
//}
import "C"
import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

type Stats struct {
	wg      *sync.WaitGroup
	Battery struct {
		Capacity uint
		Status   rune
	}
	Cpu     float64
	Nettraf struct {
		ethTag string
		rxps   uint
		runits uint
		txps   uint
		tunits uint
	}
	Ram   uint
	Time  time.Time
	Sound struct {
		btTag   string
		muteTag string
		volume  uint
	}
}

func check(e error) {
	if e != nil {
		panic(e)
	}
}

func get_battery(stats *Stats) {
	defer stats.wg.Done()

	const capacityFile = "/sys/class/power_supply/BAT1/capacity"
	const statusFile = "/sys/class/power_supply/BAT1/status"

	fd, err := os.Open(capacityFile)
	check(err)
	_, err = fmt.Fscanf(fd, "%d\n", &stats.Battery.Capacity)
	check(err)
	defer fd.Close()

	fs, err := os.Open(statusFile)
	check(err)
	var s rune
	_, err = fmt.Fscanf(fs, "%c", &s)
	check(err)
	defer fs.Close()

	switch s {
	case 'C':
		stats.Battery.Status = '+'
	case 'D':
		stats.Battery.Status = '-'
	case 'F':
		stats.Battery.Status = '='
	case 'N':
		stats.Battery.Status = '='
	default:
		stats.Battery.Status = '?'
	}
}

func get_cpu(stats *Stats) {
	// Calculate CPU active time by measuring over 1 second interval.
	defer stats.wg.Done()

	const statfile = "/proc/stat"
	var sucker uint
	var user0, system0, idle0, active0, total0 uint
	fd, err := os.Open(statfile)
	check(err)
	_, err = fmt.Fscanf(fd, "cpu %d %d %d %d", &user0, &sucker, &system0, &idle0)
	check(err)
	fd.Close()
	active0 = user0 + system0
	total0 = active0 + idle0

	time.Sleep(time.Second)

	var user1, system1, idle1, active1, total1 uint
	fd, err = os.Open(statfile)
	check(err)
	defer fd.Close()
	_, err = fmt.Fscanf(fd, "cpu %d %d %d %d", &user1, &sucker, &system1, &idle1)
	check(err)
	active1 = user1 + system1
	total1 = active1 + idle1

	stats.Cpu = float64(active1-active0) * 100 / float64(total1-total0)
}

func read_nettraf_helper(fname string) uint {
	var ret uint
	fd, err := os.Open(fname)
	check(err)
	_, err = fmt.Fscanf(fd, "%d\n", &ret)
	check(err)
	return ret
}
func get_nettraf(stats *Stats) {
	defer stats.wg.Done()
	const ETH_OPERSTATE = "/sys/class/net/enp0s25/operstate"
	var s, rxf, txf string
	var rxb0, rxb1, txb0, txb1 uint

	eo, err := os.Open(ETH_OPERSTATE)
	check(err)
	_, err = fmt.Fscanf(eo, "%s\n", &s)
	check(err)

	eth_op := s == "up"
	if eth_op {
		rxf = "/sys/class/net/enp0s25/statistics/rx_bytes"
		txf = "/sys/class/net/enp0s25/statistics/tx_bytes"
		stats.Nettraf.ethTag = "E: "
	} else {
		rxf = "/sys/class/net/wlp3s0/statistics/rx_bytes"
		txf = "/sys/class/net/wlp3s0/statistics/tx_bytes"
		stats.Nettraf.ethTag = ""
	}
	rxb0 = read_nettraf_helper(rxf)
	txb0 = read_nettraf_helper(txf)

	time.Sleep(time.Second)

	rxb1 = read_nettraf_helper(rxf)
	txb1 = read_nettraf_helper(txf)

	stats.Nettraf.rxps = (rxb1 - rxb0)
	stats.Nettraf.txps = (txb1 - txb0)

	// Rightshift 10 = division by 1024 to convert units to more readable ones
	for stats.Nettraf.runits = 0; stats.Nettraf.runits < 4 && stats.Nettraf.rxps > 1024; stats.Nettraf.runits++ {
		stats.Nettraf.rxps >>= 10
	}
	for stats.Nettraf.tunits = 0; stats.Nettraf.tunits < 4 && stats.Nettraf.txps > 1024; stats.Nettraf.tunits++ {
		stats.Nettraf.txps >>= 10
	}
}

func get_ram(stats *Stats) {
	defer stats.wg.Done()
	const ramfile = "/proc/meminfo"

	var mtotal, mfree, bf, ca uint
	fd, err := os.Open(ramfile)
	check(err)
	scanner := bufio.NewScanner(fd)

scanner:
	for scanner.Scan() {
		var field string
		var val uint
		_, err = fmt.Sscanf(scanner.Text(), "%s %d kB", &field, &val)
		check(err)
		switch field {
		case "MemTotal:":
			mtotal = val
		case "MemFree:":
			mfree = val
		case "Buffers:":
			bf = val
		case "Cached:":
			ca = val
		case "SwapCached:":
			/* Don't want to read the whole file, so stop scanning
			 * after getting the fields we need
			 */
			break scanner
		}
	}
	stats.Ram = (mtotal - (mfree + bf + ca)) >> 10
}

func get_time(stats *Stats) {
	defer stats.wg.Done()
	stats.Time = time.Now()
}

func get_volume(stats *Stats) {
	//defer stats.wg.Done()
	volcmd := exec.Command("wpctl", "get-volume", "@DEFAULT_AUDIO_SINK@")
	btcmd := exec.Command("bluetoothctl", "info")
	btLock := make(chan bool, 1)

	go func() {
		_, err := btcmd.Output()
		if err != nil {
			stats.Sound.btTag = ""
		} else {
			stats.Sound.btTag = "B"
		}
		btLock <- true
	}()

	vcout, err := volcmd.Output()
	check(err)
	toks := strings.Fields(string(vcout))
	vol, err := strconv.ParseFloat(toks[1], 64)
	check(err)
	stats.Sound.volume = uint(vol * 100)
	if len(toks) == 3 {
		stats.Sound.muteTag = "M"
	} else {
		stats.Sound.muteTag = ""
	}

	<-btLock
}

func main() {
	units := []string{"B/s", "KiB/s", "MiB/s", "GiB/s", "TiB/s"}
	stats := Stats{wg: &sync.WaitGroup{}}
	limiter := time.Tick(1500 * time.Millisecond)

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT,
	)
	sigvol := make(chan os.Signal, 1)
	signal.Notify(sigvol, syscall.SIGUSR1)
	done := false

	dpy := C.XOpenDisplay(nil)
	status := C.CString("")

	// Function to handle graceful shutdown
	go func() {
		<-sigc
		done = true
		stats.wg.Wait()
		defer C.free(unsafe.Pointer(status))
		C.XStoreName(dpy, C.def_root_win(dpy), C.CString("dwm"))
		C.XCloseDisplay(dpy)
	}()

	// Only update volume if we get the signal
	go func() {
		for {
			<-sigvol
			get_volume(&stats)
		}
	}()
	for !done {
		stats.wg.Add(5)
		go get_cpu(&stats)
		go get_nettraf(&stats)
		go get_battery(&stats)
		go get_ram(&stats)
		go get_time(&stats)

		status = C.CString(fmt.Sprintf("%s%d %s↓ %d %s↑   %s%s%d%%   %.2f%%   %d MiB   %d%%%c   %s",
			stats.Nettraf.ethTag, stats.Nettraf.rxps, units[stats.Nettraf.runits], stats.Nettraf.txps, units[stats.Nettraf.tunits],
			stats.Sound.btTag, stats.Sound.muteTag, stats.Sound.volume,
			stats.Cpu, stats.Ram, stats.Battery.Capacity, stats.Battery.Status, stats.Time.Format("Mon Jan 02 03:04 PM")))

		C.XStoreName(dpy, C.def_root_win(dpy), status)
		C.XSync(dpy, 0)

		<-limiter
		stats.wg.Wait()
	}
}
