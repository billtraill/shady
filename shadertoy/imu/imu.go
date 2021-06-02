package imu

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-gl/gl/v3.3-core/gl"

	"github.com/billtraill/shady/renderer"
	"github.com/billtraill/shady/shadertoy"
)

func init() {

	shadertoy.RegisterResourceType("imu", func(m shadertoy.Mapping, _ shadertoy.GenTexFunc, _ renderer.RenderState) (shadertoy.Resource, error) {
		return newIMUPeripheral(m.Name, m.Value)
	})
}

type imuDataType struct {
	Acceleration [3]float32 `json:"accel"`
	Gyro         [3]float32 `json:"gyro"`
	Magnetometer [3]float32 `json:"mag"`
	Quaternione  [4]float32 `json:"game_quat"`
	Shake        bool       `json:"shake"`
}

// this was matching something like
// #pragma map gyros=perip_mat4:/dev/ttyUSB0;230400?
// var (
// 	periphFile   = regexp.MustCompile(`^([^;]+)(\??)$`)
// 	periphSerial = regexp.MustCompile(`^([^;]+);(\d+)(\??)$`)
// )

//type periphMat4 struct {
type IMU_values struct {
	uniformNamePrefix  string
	url                string
	currentValue       imuDataType
	currentValueLock   sync.Mutex
	closed, loopClosed chan struct{}
}

var myClient = &http.Client{Timeout: 10 * time.Second}

func getJson(url string, target interface{}) error {
	r, err := myClient.Get(url)
	if err != nil {
		return err
	}
	defer r.Body.Close()

	return json.NewDecoder(r.Body).Decode(target)
}

func newIMUPeripheral(uniformNamePrefix, url string) (shadertoy.Resource, error) {
	var err error
	var failSilent bool

	// I'm assuming pwd probably has our url  - rename and remove the value string...

	imu := &IMU_values{
		uniformNamePrefix: uniformNamePrefix,
		url:               url,
	}
	err = getJson(url, &imu.currentValue)

	if err != nil && !failSilent {
		return nil, err
	}

	imu.closed = make(chan struct{})
	imu.loopClosed = make(chan struct{})
	go func() {
		imuReading := new(imuDataType)
		for {

			err := getJson(url, imuReading)
			if err == nil {
				imu.currentValueLock.Lock()
				imu.currentValue = *imuReading
				imu.currentValueLock.Unlock()
			}
			time.Sleep(time.Second / 10) // TODO make this configurable, value ??
		}

	}()

	return imu, nil
}

func (imu *IMU_values) UniformSource() string {

	s := ""
	s += fmt.Sprintf("uniform vec3 %sAcceleration;", imu.uniformNamePrefix)
	s += fmt.Sprintf("uniform vec3 %sGyro;", imu.uniformNamePrefix)
	s += fmt.Sprintf("uniform vec3 %sMagnetometer;", imu.uniformNamePrefix)
	s += fmt.Sprintf("uniform vec4 %sQuaternione;", imu.uniformNamePrefix)
	s += fmt.Sprintf("uniform bool %sShake;", imu.uniformNamePrefix)
	return s
}

func (imu *IMU_values) PreRender(state renderer.RenderState) {
	imu.currentValueLock.Lock()

	if loc, ok := state.Uniforms[fmt.Sprintf("%sAcceleration", imu.uniformNamePrefix)]; ok {
		gl.Uniform3f(loc.Location, float32(imu.currentValue.Acceleration[0]), float32(imu.currentValue.Acceleration[1]), float32(imu.currentValue.Acceleration[2]))
	}
	if loc, ok := state.Uniforms[fmt.Sprintf("%sGyro", imu.uniformNamePrefix)]; ok {
		gl.Uniform3f(loc.Location, float32(imu.currentValue.Gyro[0]), float32(imu.currentValue.Gyro[1]), float32(imu.currentValue.Gyro[2]))
	}

	if loc, ok := state.Uniforms[fmt.Sprintf("%sMagnetometer", imu.uniformNamePrefix)]; ok {
		gl.Uniform3f(loc.Location, float32(imu.currentValue.Magnetometer[0]), float32(imu.currentValue.Magnetometer[1]), float32(imu.currentValue.Magnetometer[2]))
	}

	if loc, ok := state.Uniforms[fmt.Sprintf("%sQuaternione", imu.uniformNamePrefix)]; ok {
		gl.Uniform4f(loc.Location, float32(imu.currentValue.Quaternione[0]), float32(imu.currentValue.Quaternione[1]), float32(imu.currentValue.Quaternione[2]), float32(imu.currentValue.Quaternione[3]))
	}

	if loc, ok := state.Uniforms[fmt.Sprintf("%sShake", imu.uniformNamePrefix)]; ok {
		var shake int32 = 0
		if imu.currentValue.Shake {
			shake = 1
		}
		gl.Uniform1i(loc.Location, int32(shake))
	}

	imu.currentValueLock.Unlock()
}

func (imu *IMU_values) Close() error {
	if imu.closed == nil {
		return nil
	}
	close(imu.closed)
	<-imu.loopClosed
	return nil
}
