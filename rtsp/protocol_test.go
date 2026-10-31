package rtsp

import (
	"bufio"
	"strings"
	"testing"
)

func TestRejectsInvalidBodyLengthsWithoutPanic(t *testing.T) {
	for _, length := range []string{"-1", "999999999999999", "2\r\nContent-Length: 3"} {
		t.Run(length, func(t *testing.T) {
			req := new(Request)
			err := req.Read(bufio.NewReader(strings.NewReader("SETUP /x RTSP/1.0\r\nCSeq: 1\r\nContent-Length: " + length + "\r\n\r\nabc")))
			if err == nil {
				t.Fatal("accepted invalid or ambiguous length")
			}
		})
	}
}
func TestOptionsAsterisk(t *testing.T) {
	req := new(Request)
	if err := req.Read(bufio.NewReader(strings.NewReader("OPTIONS * RTSP/1.0\r\nCSeq: 1\r\n\r\n"))); err != nil {
		t.Fatal(err)
	}
}
