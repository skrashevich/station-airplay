package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"github.com/grandcat/zeroconf"
	"github.com/skrashevich/station-airplay/audio"
	"github.com/skrashevich/station-airplay/codec"
	"github.com/skrashevich/station-airplay/config"
	"github.com/skrashevich/station-airplay/event"
	"github.com/skrashevich/station-airplay/handlers"
	"github.com/skrashevich/station-airplay/homekit"
	"github.com/skrashevich/station-airplay/ptp"
	"github.com/skrashevich/station-airplay/rtsp"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

var version = "dev"

type options struct {
	name, iface, listen, state, aplay, device string
	delay                                     time.Duration
	volume                                    float64
	check                                     bool
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.LUTC)
	var o options
	name, _ := os.Hostname()
	if name == "" {
		name = "Station"
	}
	flag.StringVar(&o.name, "name", name+" AirPlay", "Name shown in the AirPlay output list")
	flag.StringVar(&o.iface, "interface", "", "Network interface (automatically selected by default)")
	flag.StringVar(&o.listen, "listen", ":7000", "IPv4 RTSP address and port")
	flag.StringVar(&o.state, "data-dir", "/data/opt/station-airplay/state", "Persistent identity and pairing directory")
	flag.StringVar(&o.aplay, "aplay", "aplay", "Existing firmware aplay executable")
	flag.StringVar(&o.device, "device", "default", "ALSA playback PCM; default is portable across tested Stations")
	flag.DurationVar(&o.delay, "output-delay", 160*time.Millisecond, "Estimated audio output latency")
	flag.Float64Var(&o.volume, "volume", -20, "Initial AirPlay volume in dB (-144 mutes, maximum 0)")
	flag.BoolVar(&o.check, "check", false, "Check prerequisites without opening audio or network listeners")
	showVersion := flag.Bool("version", false, "Print build version")
	flag.Parse()
	if *showVersion {
		fmt.Printf("station-airplay %s (pure Go, firmware aplay output)\n", version)
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, o); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func selectInterface(name string) (net.Interface, net.IP, error) {
	list, err := net.Interfaces()
	if err != nil {
		return net.Interface{}, nil, err
	}
	var route net.IP
	if name == "" {
		if c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}); err == nil {
			route = c.LocalAddr().(*net.UDPAddr).IP
			c.Close()
		}
	}
	var fallback *net.Interface
	var fallbackIP net.IP
	for _, iface := range list {
		if name != "" && iface.Name != name {
			continue
		}
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			ip, _, err := net.ParseCIDR(a.String())
			if err != nil || ip.To4() == nil || !ip.IsGlobalUnicast() {
				continue
			}
			if name != "" || ip.Equal(route) {
				return iface, ip.To4(), nil
			}
			if fallback == nil {
				i := iface
				fallback = &i
				fallbackIP = ip.To4()
			}
		}
	}
	if fallback != nil {
		return *fallback, fallbackIP, nil
	}
	return net.Interface{}, nil, fmt.Errorf("no usable IPv4 multicast interface %q", name)
}
func run(ctx context.Context, o options) error {
	if strings.TrimSpace(o.name) == "" || len(o.name) > 63 || o.volume > 0 || o.volume < -144 || o.volume != o.volume || o.delay < 0 || o.delay > 5*time.Second {
		return fmt.Errorf("invalid name, volume or output delay")
	}
	aplay, err := exec.LookPath(o.aplay)
	if err != nil {
		return fmt.Errorf("firmware aplay is required: %w", err)
	}
	iface, ip, err := selectInterface(o.iface)
	if err != nil {
		return err
	}
	if o.check {
		fmt.Printf("version=%s interface=%s ip=%s aplay=%s pcm=%s state=%s\n", version, iface.Name, ip, aplay, o.device, o.state)
		return nil
	}
	id, err := config.LoadIdentity(o.state)
	if err != nil {
		return err
	}
	homekit.Server, err = homekit.NewServer(id.DeviceID, o.state, []string{ip.String()})
	if err != nil {
		return err
	}
	homekit.Server.Name = o.name
	homekit.Device = homekit.NewAccessory(id.DeviceID, id.UUID, airplayDevice(), homekit.Server.Device.PublicKey())
	host, port, err := net.SplitHostPort(o.listen)
	if err != nil {
		return err
	}
	if host == "" {
		host = ip.String()
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort(host, port))
	if err != nil {
		return fmt.Errorf("RTSP listen: %w", err)
	}
	defer listener.Close()
	eventListener, err := net.Listen("tcp4", net.JoinHostPort(ip.String(), "0"))
	if err != nil {
		return err
	}
	defer eventListener.Close()
	homekit.Server.EventPort = uint16(eventListener.Addr().(*net.TCPAddr).Port)
	clock := ptp.NewVirtualClock(0)
	timing, err := ptp.Listen(clock, ip, id.ClockID())
	if err != nil {
		return err
	}
	defer timing.Close()
	player := audio.NewPlayer(ctx, clock, codec.OutputOptions{Command: aplay, Device: o.device})
	player.BindIP = ip.String()
	player.Timing = timing
	player.OutputDelay = o.delay
	player.SetVolume(o.volume)
	txt := homekit.Device.ToRecords()
	actualPort := listener.Addr().(*net.TCPAddr).Port
	mdns, err := zeroconf.Register(o.name, "_airplay._tcp", "local.", actualPort, txt, []net.Interface{iface})
	if err != nil {
		return fmt.Errorf("mDNS: %w", err)
	}
	defer mdns.Shutdown()
	raopTXT := []string{"ch=2", "cn=2", "et=4", "sr=44100", "ss=16", "tp=TCP", "vn=65537", "vs=366.0", "am=StationAirPlay", "sf=0x4", "ft=" + airplayDevice().ToRecord(), "pk=" + hex.EncodeToString(homekit.Server.Device.PublicKey())}
	raop, err := zeroconf.Register(strings.ReplaceAll(id.DeviceID, ":", "")+"@"+o.name, "_raop._tcp", "local.", actualPort, raopTXT, []net.Interface{iface})
	if err != nil {
		return fmt.Errorf("RAOP mDNS: %w", err)
	}
	defer raop.Shutdown()
	localCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Go(func() { event.Serve(localCtx, eventListener) })
	wg.Go(func() { timing.Serve(localCtx) })
	defer wg.Wait()
	defer cancel()
	log.Printf("ready name=%q interface=%s address=%s event=%d codec=AAC-LC output=%s device=%s state=%s", o.name, iface.Name, listener.Addr(), homekit.Server.EventPort, filepath.Base(aplay), o.device, o.state)
	return rtsp.Serve(localCtx, listener, func() (rtsp.Handler, error) { return handlers.NewRstpHandler(o.name, player) })
}
