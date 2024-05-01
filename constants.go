package motionmount

import "tinygo.org/x/bluetooth"

const vogelsCompanyID = 3391

var motionMountServiceUUID = must(bluetooth.ParseUUID("3e6fe65d-ed78-11e4-895e-00026fd5c52c"))
var setPositionCharacteristicUUID = must(bluetooth.ParseUUID("c005fa21-0651-4800-b000-000000000000"))
var wallDistanceCharacteristic = must(bluetooth.ParseUUID("c005fa00-0651-4800-b000-000000000000"))
var orientationCharacteristic = must(bluetooth.ParseUUID("c005fa01-0651-4800-b000-000000000000"))
var positionCharacteristicUUIDs = []bluetooth.UUID{
	must(bluetooth.ParseUUID("c005fa0a-0651-4800-b000-000000000000")), // 00
	must(bluetooth.ParseUUID("c005fa0b-0651-4800-b000-000000000000")), // 01
	must(bluetooth.ParseUUID("c005fa0c-0651-4800-b000-000000000000")), // 02
	must(bluetooth.ParseUUID("c005fa0d-0651-4800-b000-000000000000")), // 03
	must(bluetooth.ParseUUID("c005fa0e-0651-4800-b000-000000000000")), // 04
	must(bluetooth.ParseUUID("c005fa0f-0651-4800-b000-000000000000")), // 05
	must(bluetooth.ParseUUID("c005fa10-0651-4800-b000-000000000000")), // 06

	must(bluetooth.ParseUUID("c005fa17-0651-4800-b000-000000000000")), // 07
	must(bluetooth.ParseUUID("c005fa18-0651-4800-b000-000000000000")), // 08
	must(bluetooth.ParseUUID("c005fa19-0651-4800-b000-000000000000")), // 09
	must(bluetooth.ParseUUID("c005fa1a-0651-4800-b000-000000000000")), // 10
	must(bluetooth.ParseUUID("c005fa1b-0651-4800-b000-000000000000")), // 11
	must(bluetooth.ParseUUID("c005fa1c-0651-4800-b000-000000000000")), // 12
	must(bluetooth.ParseUUID("c005fa1d-0651-4800-b000-000000000000")), // 13
}

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}

	return value
}
