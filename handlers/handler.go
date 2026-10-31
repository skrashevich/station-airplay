// Adapted from goplay2; every control connection owns its handler and streams.
package handlers

import (
	"github.com/brutella/hc/hap/pair"
	"github.com/skrashevich/station-airplay/audio"
	"github.com/skrashevich/station-airplay/homekit"
	"github.com/skrashevich/station-airplay/pairing"
	"github.com/skrashevich/station-airplay/rtsp"
	"log"
	"net"
)

type Rstp struct {
	streams   map[string]*audio.Server
	pairing   *pairing.Controller
	player    *audio.Player
	peerIP    net.IP
	volume    float64
	transient *pair.SetupServerSession
}

func NewRstpHandler(_ string, p *audio.Player) (*Rstp, error) {
	return &Rstp{streams: make(map[string]*audio.Server), pairing: pairing.NewController(homekit.Server.Database), player: p, volume: -20}, nil
}
func (r *Rstp) OnConnOpen(c *rtsp.Conn) {
	r.peerIP = c.NetConn().RemoteAddr().(*net.TCPAddr).IP
	c.SetNetConn(homekit.NewConnection(c.NetConn(), homekit.Server.Context))
}
func (r *Rstp) OnConnClose(c *rtsp.Conn) {
	for _, s := range r.streams {
		s.Close()
	}
	homekit.Server.Context.DeleteSessionForConnection(c.NetConn())
}
func (r *Rstp) OnRequest(_ *rtsp.Conn, req *rtsp.Request) {
	log.Printf("RTSP %s /%s", req.Method, req.Path)
}
func (r *Rstp) OnResponse(_ *rtsp.Conn, _ *rtsp.Response) {}
func (r *Rstp) Handle(c *rtsp.Conn, req *rtsp.Request) (response *rtsp.Response, failure error) {
	defer func() {
		if recover() != nil {
			response = &rtsp.Response{StatusCode: rtsp.StatusBadRequest}
			failure = nil
		}
	}()
	if req.Method == "OPTIONS" {
		return &rtsp.Response{StatusCode: rtsp.StatusOK, Header: rtsp.Header{"Public": {"GET, POST, SETUP, GET_PARAMETER, SET_PARAMETER, RECORD, SETPEERS, SETRATEANCHORTIME, FLUSHBUFFERED, TEARDOWN"}}}, nil
	}
	if req.Method == "GET" {
		return r.OnGetWeb(req)
	}
	if req.Method == "POST" && (req.Path == "pair-setup" || req.Path == "pair-verify") {
		return r.OnPostWeb(c, req)
	}
	session := homekit.Server.Context.GetSessionForConnection(c.NetConn())
	if session == nil || session.Encrypter() == nil {
		return &rtsp.Response{StatusCode: rtsp.StatusConnectionAuthorizationRequired}, nil
	}
	switch req.Method {
	case "POST":
		return r.OnPostWeb(c, req)
	case "SETUP":
		return r.OnSetupWeb(req)
	case "GET_PARAMETER":
		return r.OnGetParameterWeb(req)
	case "SET_PARAMETER":
		return r.OnSetParameterWeb(req)
	case "RECORD":
		return r.OnRecordWeb(req)
	case "SETPEERS", "SETPEERSX":
		return r.OnSetPeerWeb(req)
	case "SETRATEANCHORTIME":
		return r.OnSetRateAnchorTime(req)
	case "FLUSHBUFFERED":
		return r.OnFlushBuffered(req)
	case "TEARDOWN":
		return r.OnTeardownWeb(req)
	}
	return &rtsp.Response{StatusCode: rtsp.StatusNotImplemented}, nil
}
