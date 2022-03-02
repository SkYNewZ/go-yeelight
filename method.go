package yeelight

import "fmt"

var _ fmt.Stringer = (*Method)(nil)

// Method describes the method string to send to a yeelight device.
type Method string

func (m Method) String() string {
	return string(m)
}

// Currently supported commands
const (
	SetColorTemperatureABX Method = "set_ct_abx"
	SetRGB                 Method = "set_rgb"
	SetHSV                 Method = "set_hsv"
	SetBrightness          Method = "set_bright"
	SetPower               Method = "set_power"
	Toggle                 Method = "toggle"
	GetProp                Method = "get_prop"
	Props                  Method = "props"
	AdjustBrightness       Method = "adjust_bright"
	AdjustColorTemperature Method = "adjust_ct"
)
