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

func TestSingleBatteryJSONAndDisplay(t *testing.T) {
	value := SingleBattery(BatteryComponent{Level: 0, Charging: true})
	encoded, _ := json.Marshal(value)
	if string(encoded) != `{"level":0,"charging":true}` {
		t.Fatalf("battery JSON = %s", encoded)
	}
	if got := value.Display(); got != "0% charging" {
		t.Fatalf("display=%q", got)
	}
	parts := BatteryValue{Left: &BatteryComponent{Level: 89}, Case: &BatteryComponent{Level: 42, Charging: true}}
	if got := parts.Display(); got != "left 89% · case 42% charging" {
		t.Fatalf("display=%q", got)
	}
}
