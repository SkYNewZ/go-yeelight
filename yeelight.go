package yeelight

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	// Interface guards
	_ Yeelight = (*yeelight)(nil)

	// ErrDiscoverNoDeviceFound is returned when no device is found when discovering
	ErrDiscoverNoDeviceFound = errors.New("no device found")
)

const (
	crlf        = "\r\n" // each command and result must end with this characters
	discoverMSG = "M-SEARCH * HTTP/1.1\r\n HOST:239.255.255.250:1982\r\n MAN:\"ssdp:discover\"\r\n ST:wifi_bulb\r\n"
	ssdpAddr    = "239.255.255.250:1982"
)

// Yeelight is a light device you want to control
type Yeelight interface {
	fmt.Stringer

	// On turns on the yeelight
	On() error

	// Off turns the yeelight
	Off() error

	// SetColorTemperature will set the yeelight color temperature
	SetColorTemperature(temperature int) error

	// SetRGB will set yeelight red, green and blue values
	SetRGB(red, green, blue uint8) error

	// SetBrightness will set the yeelight brightness.
	SetBrightness(brightness int) error

	// IsPowerOn return whether the yeelight is power on
	IsPowerOn() (bool, error)

	// Toggle on or off the Yeelight
	Toggle() error

	// Listen for events on current Yeelight
	Listen(ctx context.Context) (<-chan *Notification, error)

	// AdjustBrightness adjust the brightness by specified percentage within specified duration.
	// The percentage range is: (-100,100).
	// duration is in milliseconds and minimum is 30ms.
	AdjustBrightness(percentage int, duration int) error

	// AdjustColorTemperature adjust the color temperature by specified percentage within specified duration.
	// The percentage range is: (-100,100).
	// duration is in milliseconds and minimum is 30ms.
	AdjustColorTemperature(percentage int, duration int) error
}

type (
	yeelight struct {
		mu      sync.Mutex
		address string     // ip address
		rnd     *rand.Rand // random seed for commands
	}

	// request body to send to a yeelight device
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

func (y *yeelight) String() string {
	return y.address
}

// Discover tries to discover a Yeelight on your local network
func Discover() (Yeelight, error) {
	ssdp, err := net.ResolveUDPAddr("udp4", ssdpAddr)
	if err != nil {
		return nil, err
	}

	pc, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil, err
	}

	socket := pc.(*net.UDPConn)
	if _, err := socket.WriteToUDP([]byte(discoverMSG), ssdp); err != nil {
		return nil, err
	}

	if err := socket.SetReadDeadline(time.Now().Add(time.Second * 3)); err != nil {
		return nil, err
	}

	buf := make([]byte, 1024)
	size, _, err := socket.ReadFromUDP(buf)
	if err != nil {
		return nil, ErrDiscoverNoDeviceFound
	}

	if err := pc.Close(); err != nil {
		return nil, err
	}

	rs := buf[0:size]
	addr := parseAddr(string(rs))
	return New(addr)
}

// parseAddr parses address from ssdp response
func parseAddr(msg string) string {
	if strings.HasSuffix(msg, crlf) {
		msg = msg + crlf
	}
	resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(msg)), nil)
	if err != nil {
		return ""
	}

	defer resp.Body.Close()
	return strings.TrimPrefix(resp.Header.Get("LOCATION"), "yeelight://")
}

// New creates a new yeelight object.
func New(address string) (Yeelight, error) {
	if !strings.Contains(address, ":") {
		address += ":55443"
	}

	return &yeelight{
		mu:      sync.Mutex{},
		address: address,
		rnd:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}, nil
}

// send given Method and args to current yeelight device
func (y *yeelight) send(method Method, args ...interface{}) (*Response, error) {
	y.mu.Lock()
	defer y.mu.Unlock()

	cmd := command{
		ID:     y.rnd.Intn(100),
		Method: method,
		Params: args,
	}

	conn, err := net.Dial("tcp", y.address)
	if err != nil {
		return nil, fmt.Errorf("[%s] could not dial address: %w", y.address, err)
	}
	defer conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(time.Second * 2)); err != nil {
		return nil, fmt.Errorf("[%s] could not set read deadline: %w", y.address, err)
	}

	if err := json.NewEncoder(conn).Encode(cmd); err != nil {
		return nil, fmt.Errorf("[%s] could not encode command: %w", y.address, err)
	}

	if _, err := fmt.Fprint(conn, crlf); err != nil {
		return nil, fmt.Errorf("[%s] cannot write trailer: %w", y.address, err)
	}

	// Wait and read for Response
	var rs Response
	if err := json.NewDecoder(conn).Decode(&rs); err != nil {
		return nil, fmt.Errorf("[%s] cannot parse command result %w", y.address, err)
	}

	if rs.Error != nil {
		return nil, fmt.Errorf("[%s] command execution error, received %v", y.address, rs.Error)
	}

	return &rs, nil
}

func (y *yeelight) Listen(ctx context.Context) (<-chan *Notification, error) {
	var notificationsCh = make(chan *Notification)
	conn, err := net.DialTimeout("tcp", y.address, time.Second*3)
	if err != nil {
		return nil, fmt.Errorf("[%s] could not dial address: %w", y.address, err)
	}

	go func(c net.Conn) {
		defer close(notificationsCh)
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
	_, err := y.send(SetPower, "on")
	return err
}

func (y *yeelight) Off() error {
	_, err := y.send(SetPower, "off")
	return err
}

func (y *yeelight) SetColorTemperature(temperature int) error {
	switch {
	case temperature < 1700:
		temperature = 1700
	case temperature > 6500:
		temperature = 6500
	}
	_, err := y.send(SetColorTemperatureABX, temperature)
	return err
}

func (y *yeelight) SetRGB(red, green, blue uint8) error {
	r := uint32(red) << 16
	g := uint32(green) << 8
	b := uint32(blue)
	_, err := y.send(SetRGB, r+g+b)
	return err
}

func (y *yeelight) SetBrightness(brightness int) error {
	switch {
	case brightness > 100:
		brightness = 100
	case brightness < 1:
		brightness = 1
	}

	_, err := y.send(SetBrightness, brightness)
	return err
}

func (y *yeelight) IsPowerOn() (bool, error) {
	resp, err := y.send(GetProp, "power")
	if err != nil {
		return false, err
	}

	return resp.Result[0] == "on", err
}

func (y *yeelight) Toggle() error {
	_, err := y.send(Toggle)
	return err
}

func (y *yeelight) AdjustBrightness(percentage int, duration int) error {
	switch {
	case percentage > 100:
		percentage = 100
	case percentage < -100:
		percentage = -100
	}

	// minimum duration value
	if duration < 30 {
		duration = 30
	}

	_, err := y.send(AdjustBrightness, percentage, duration)
	return err
}

func (y *yeelight) AdjustColorTemperature(percentage int, duration int) error {
	switch {
	case percentage > 100:
		percentage = 100
	case percentage < -100:
		percentage = -100
	}

	// minimum duration value
	if duration < 30 {
		duration = 30
	}

	_, err := y.send(AdjustColorTemperature, percentage, duration)
	return err
}
