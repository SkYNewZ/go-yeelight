package yeelight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"
)

// Interface guard
var _ Yeelight = (*yeelight)(nil)

// Yeelight is a light device you want to control
type Yeelight interface {
	// Send given Method and args to current yeelight device
	Send(method Method, args ...interface{}) (*Response, error)

	// On turns on the yeelight
	On() error

	// Off turns off the yeelight
	Off() error

	// SetColorTemperature will set the yeelight color temperature
	SetColorTemperature(temp uint16) error

	// SetRGB will set yeelight red, green and blue values
	SetRGB(red, green, blue uint8) error

	// GetRGB returns the current yeelight RGB value
	GetRGB() (uint8, uint8, uint8, error)

	// SetBrightness will set the yeelight brightness.
	SetBrightness(brightness uint8) error

	// IsPowerOn return whether the yeelight is power on
	IsPowerOn() (bool, error)

	// Toggle on or off the Yeelight
	Toggle() error

	// Listen for events that happend on current Yeelight
	Listen(ctx context.Context) (<-chan *Notification, error)
}

type (
	// Method describes the method string to send to a yeelight
	Method string

	yeelight struct {
		sync.Mutex
		name string
		addr string     // ip address
		rnd  *rand.Rand // random seed for commands
	}

	// request body to Send to a yeelight device
	command struct {
		// ID is filled by message sender.
		// It will be echoed back in RESULT message.
		// This is to help request sender to correlate request and Response.
		ID     int           `json:"id"`
		Method Method        `json:"method"`
		Params []interface{} `json:"params"`
	}

	// Notification describes a change on Yeelight
	Notification struct {
		Method Method            `json:"method"`
		Params map[string]string `json:"params"`
	}

	// Response describes command response from a yeelight device
	Response struct {
		ID     int           `json:"id"`
		Result []interface{} `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
)

// Currently supported commands
const (
	SetCTABX      Method = "set_ct_abx"
	SetRGB        Method = "set_rgb"
	SetHSV        Method = "set_hsv"
	SetBrightness Method = "set_bright"
	SetPower      Method = "set_power"
	Toggle        Method = "toggle"
	GetProp       Method = "get_prop"
	Props         Method = "props"
)

// each command and result end with this characters
const (
	crlf        = "\r\n"
	discoverMSG = "M-SEARCH * HTTP/1.1\r\n HOST:239.255.255.250:1982\r\n MAN:\"ssdp:discover\"\r\n ST:wifi_bulb\r\n"
	ssdpAddr    = "239.255.255.250:1982"
)

// Discover tries to discover a Yeelight on your local network
func Discover() error {
	addr, err := net.ResolveUDPAddr("udp4", ssdpAddr)
	if err != nil {
		return err
	}

	pc, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return err
	}

	socket := pc.(*net.UDPConn)
	if _, err := socket.WriteToUDP([]byte(discoverMSG), addr); err != nil {
		return err
	}

	_ = socket.SetReadDeadline(time.Now().Add(time.Second * 3))
	buf := make([]byte, 1024)
	n, _, err := socket.ReadFromUDP(buf)
	if err != nil {
		return errors.New("no device found")
	}

	fmt.Printf("%s sent this: %s\n", addr, buf[:n])
	return pc.Close()
}

// New creates a new yeelight object.
func New(address, name string) (Yeelight, error) {
	if !strings.Contains(address, ":") {
		address += ":55443"
	}

	if name == "" {
		name = address
	}

	return &yeelight{
		name:  name,
		addr:  address,
		rnd:   rand.New(rand.NewSource(time.Now().UnixNano())),
		Mutex: sync.Mutex{},
	}, nil
}

// Send given Method and args to current yeelight device
func (y *yeelight) Send(method Method, args ...interface{}) (*Response, error) {
	y.Lock()
	defer y.Unlock()

	cmd := command{
		ID:     y.rnd.Intn(100),
		Method: method,
		Params: args,
	}

	conn, err := net.Dial("tcp", y.addr)
	if err != nil {
		return nil, fmt.Errorf("[%s] cannot dial with %s: %w", y.name, y.addr, err)
	}
	defer conn.Close()

	b, _ := json.Marshal(cmd)
	if _, err := fmt.Fprint(conn, string(b)+crlf); err != nil {
		return nil, fmt.Errorf("[%s] cannot execute command: %w", y.name, err)
	}

	// Wait and read for Response
	var rs Response
	if err := json.NewDecoder(conn).Decode(&rs); err != nil {
		return nil, fmt.Errorf("[%s] cannot parse command result %w", y.name, err)
	}

	if rs.Error != nil {
		return nil, fmt.Errorf("[%s] command execution error, received %v", y.name, rs.Error)
	}

	return &rs, nil
}

func (y *yeelight) Listen(ctx context.Context) (<-chan *Notification, error) {
	conn, err := net.DialTimeout("tcp", y.addr, time.Second*3)
	if err != nil {
		return nil, fmt.Errorf("[%s] cannot dial with %s: %w", y.name, y.addr, err)
	}

	var notificationsCh = make(chan *Notification)
	go func(c net.Conn) {
		defer c.Close()

		for {
			select {
			case <-ctx.Done():
				return
			default:
				// Wait and read for Notification
				var notif Notification
				if err := json.NewDecoder(c).Decode(&notif); err == nil {
					notificationsCh <- &notif
				}
			}
		}
	}(conn)

	return notificationsCh, nil
}

func (y *yeelight) On() error {
	_, err := y.Send(SetPower, "on")
	return err
}

func (y *yeelight) Off() error {
	_, err := y.Send(SetPower, "off")
	return err
}

func (y *yeelight) SetColorTemperature(temp uint16) error {
	switch {
	case temp < 1700:
		temp = 1700
	case temp > 6500:
		temp = 6500
	}
	_, err := y.Send(SetCTABX, temp)
	return err
}

func (y *yeelight) SetRGB(red, green, blue uint8) error {
	r := uint32(red) << 16
	g := uint32(green) << 8
	b := uint32(blue)
	_, err := y.Send(SetRGB, r+g+b)
	return err
}

func (y *yeelight) GetRGB() (uint8, uint8, uint8, error) {
	panic("implement me")
}

func (y *yeelight) SetBrightness(brightness uint8) error {
	switch {
	case brightness > 100:
		brightness = 100
	case brightness < 1:
		brightness = 1
	}
	_, err := y.Send(SetBrightness, brightness)
	return err
}

func (y *yeelight) IsPowerOn() (bool, error) {
	resp, err := y.Send(GetProp, "power")
	if err != nil {
		return false, err
	}

	return resp.Result[0] == "on", err
}

func (y *yeelight) Toggle() error {
	_, err := y.Send(Toggle)
	return err
}
