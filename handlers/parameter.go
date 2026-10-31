// Adapted from goplay2; volume applies only to this receiver's PCM.
package handlers

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/skrashevich/station-airplay/rtsp"
	"math"
	"strconv"
	"strings"
)

func (r *Rstp) OnGetParameterWeb(req *rtsp.Request) (*rtsp.Response, error) {
	return &rtsp.Response{StatusCode: rtsp.StatusOK, Header: rtsp.Header{"Content-Type": {"text/parameters"}}, Body: []byte(fmt.Sprintf("volume: %.6f\r\n", r.volume))}, nil
}
func (r *Rstp) OnSetParameterWeb(req *rtsp.Request) (*rtsp.Response, error) {
	scanner := bufio.NewScanner(bytes.NewReader(req.Body))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "volume:") {
			v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, "volume:")), 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v > 0 || v < -144 {
				return &rtsp.Response{StatusCode: rtsp.StatusBadRequest}, nil
			}
			if len(r.streams) == 0 {
				return &rtsp.Response{StatusCode: rtsp.StatusMethodNotValidInThisState}, nil
			}
			r.volume = v
			r.player.SetVolume(v)
		}
	}
	return &rtsp.Response{StatusCode: rtsp.StatusOK}, nil
}
