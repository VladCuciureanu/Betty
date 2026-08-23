package driver

import (
	"encoding/json"
	"testing"
)

func TestBatteryValueJSONContract(t *testing.T) {
	value := BatteryValue{
		Left:  &BatteryComponent{Level: 89, Status: "discharging"},
		Right: &BatteryComponent{Level: 88, Charging: true, Status: "charging"},
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	const expected = `{"left":{"level":89,"status":"discharging"},"right":{"level":88,"charging":true,"status":"charging"}}`
	if string(encoded) != expected {
		t.Fatalf("battery JSON = %s", encoded)
	}
}
