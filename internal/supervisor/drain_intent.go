package supervisor

import (
	"errors"
	"path/filepath"
)

type DrainIntent struct {
	ID, Instance, DeviceID, BuildID string
	Queues                          []string
}

func SaveDrainIntent(directory string, in DrainIntent) error {
	if !safeID(in.ID) || !safeID(in.Instance) {
		return errors.New("invalid drain intent identity")
	}
	return atomicJSON(filepath.Join(directory, "supervisor", "intents", in.ID+".json"), in)
}
func ReadDrainIntent(directory, id string) (DrainIntent, error) {
	var in DrainIntent
	if !safeID(id) {
		return in, errors.New("invalid drain intent identity")
	}
	err := readJSON(filepath.Join(directory, "supervisor", "intents", id+".json"), &in)
	if err == nil && in.ID != id {
		err = errors.New("drain intent mismatch")
	}
	return in, err
}
