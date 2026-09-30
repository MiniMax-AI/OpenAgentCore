package sessions

import (
	"errors"
	"testing"
)

// A Session that is already bound to a device never gets a second one.
func TestCreateEnvironmentDevice(t *testing.T) {
	device := ExecutionDevice{ID: "device", Name: "runtime", EnvironmentID: "environment"}
	free := &fakeTx{t: t, loadBoundDevice: returns(false), insertEnvironmentDevice: done}
	if err := CreateEnvironmentDevice(t.Context(), free, device, "hash"); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, free, "LoadBoundDevice", "InsertEnvironmentDevice device runtime environment hash")

	bound := &fakeTx{t: t, loadBoundDevice: returns(true)}
	if err := CreateEnvironmentDevice(t.Context(), bound, device, "hash"); !errors.Is(err, ErrDeviceBindingConflict) {
		t.Fatal(err)
	}
	assertCalls(t, bound, "LoadBoundDevice")
}
