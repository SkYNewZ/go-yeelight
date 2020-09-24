package yeelight

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"
)

type (
	// command is send to the light yeelight.
	command struct {
		ID     int           `json:"id"`
		Method string        `json:"method"`
		Params []interface{} `json:"params"`
	}

	// commandResult represents response from Yeelight device
	commandResult struct {
		ID     int           `json:"id"`
		Result []interface{} `json:"result,omitempty"`
		Error  *Error        `json:"error,omitempty"`
	}

	//Error struct represents error part of response
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	// Yeelight struct is used to control the lights.
	Yeelight struct {
		mu   sync.Mutex
		addr string
		rnd  *rand.Rand
		Name string
	}

	// method describes the method to send to the light yeelight.
	method string
)

const (
	// timeout value for TCP and UDP commands
	timeout = time.Second * 1

	// CR-LF delimiter
	crlf = "\r\n"
)

var (
	MethodSetCTABX      method = "set_ct_abx"
	MethodSetRGB        method = "set_rgb"
	MethodSetHSV        method = "set_hsv"
	MethodSetBrightness method = "set_bright"
	MethodSetPower      method = "set_power"
	MethodToggle        method = "toggle"
	MethodGetProp       method = "get_prop"
)

// Convert a method to string
func (m *method) String() string {
	if m == nil {
		return ""
	}
	return string(*m)
}

// New creates a new Yeelight object.
func New(address, name string) (*Yeelight, error) {
	if !strings.Contains(address, ":") {
		address += ":55443"
	}

	return &Yeelight{
		addr: address,
		rnd:  rand.New(rand.NewSource(time.Now().UnixNano())),
		Name: name,
	}, nil
}

func (y *Yeelight) randID() int {
	i := y.rnd.Intn(100)
	return i
}

func (y *Yeelight) send(method method, args ...interface{}) (*commandResult, error) {
	y.mu.Lock()
	defer y.mu.Unlock()

	// Create command
	cmd := command{
		ID:     y.randID(),
		Method: method.String(),
		Params: args,
	}

	conn, err := net.Dial("tcp", y.addr)
	if err != nil {
		return nil, fmt.Errorf("could not dial address: %s", err)
	}
	defer conn.Close()

	time.Sleep(time.Second)
	conn.SetReadDeadline(time.Now().Add(timeout))

	// Write command
	b, _ := json.Marshal(cmd)
	fmt.Fprint(conn, string(b)+crlf)

	// Wait and read for response
	var rs commandResult
	res, err := bufio.NewReader(conn).ReadString('\n')
	err = json.Unmarshal([]byte(res), &rs)

	if nil != err {
		return nil, fmt.Errorf("cannot parse command result %s", err)
	}
	if nil != rs.Error {
		return nil, fmt.Errorf("command execution error. Code: %d, Message: %s", rs.Error.Code, rs.Error.Message)
	}

	return &rs, nil
}

// TurnOn will turn the light yeelight on.
func (y *Yeelight) TurnOn() error {
	_, err := y.send(MethodSetPower, "on")
	return err
}

// TurnOff will turn the light yeelight off.
func (y *Yeelight) TurnOff() error {
	_, err := y.send(MethodSetPower, "off")
	return err
}

// ColorTemp will set the light yeelight color temperature
func (y *Yeelight) ColorTemp(temp int) error {
	switch {
	case temp < 1700:
		temp = 1700
	case temp > 6500:
		temp = 6500
	}
	_, err := y.send(MethodSetCTABX, temp)
	return err
}

// RGB will set the light yeelight red, green and blue values.
func (y *Yeelight) RGB(red, green, blue int) error {
	_, err := y.send(MethodSetRGB, red<<16+green<<8+blue)
	return err
}

// Brightness will set the light yeelight brightness.
func (y *Yeelight) Brightness(brightness int) error {
	switch {
	case brightness > 100:
		brightness = 100
	case brightness < 1:
		brightness = 1
	}
	_, err := y.send(MethodSetBrightness, brightness)
	return err
}

// IsPowerOn return whether the yeelight is power on
func (y *Yeelight) IsPowerOn() (bool, error) {
	resp, err := y.send(MethodGetProp, "power")
	if err != nil {
		return false, err
	}

	return resp.Result[0] == "on", err
}
