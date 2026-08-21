//go:build darwin

package main

import "testing"

func TestParseMobileBluetoothPlist(t *testing.T) {
	plist := []byte(`<?xml version="1.0"?><plist><dict>
<key>MagicAccIRK</key><string>71-44-65-1E-E2-CA-D1-26-F2-4E-20-EE-3E-CC-DE-77</string>
<key>MagicAccEncKey</key><string>3A-9C-81-57-E6-44-7F-F2-FB-8E-07-63-6C-99-E1-29</string>
</dict></plist>`)
	values, err := parseMobileBluetoothPlist(plist)
	if err != nil {
		t.Fatal(err)
	}
	irk, err := normalizeSystemKey(values["MagicAccIRK"])
	if err != nil || irk != "7144651ee2cad126f24e20ee3eccde77" {
		t.Fatalf("irk=%q err=%v", irk, err)
	}
	enc, err := normalizeSystemKey(values["MagicAccEncKey"])
	if err != nil || enc != "3a9c8157e6447ff2fb8e07636c99e129" {
		t.Fatalf("enc=%q err=%v", enc, err)
	}
}
