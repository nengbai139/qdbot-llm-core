//go:build !darwin && !linux && !windows

package daemon

import "errors"

type unsupportedDaemon struct{}

func newPlatformDaemon(name string) Daemon { return &unsupportedDaemon{} }

func (d *unsupportedDaemon) Install(string, []string) error {
	return errors.New("daemon: unsupported platform")
}
func (d *unsupportedDaemon) Uninstall() error        { return errors.New("daemon: unsupported platform") }
func (d *unsupportedDaemon) Start() error            { return errors.New("daemon: unsupported platform") }
func (d *unsupportedDaemon) Stop() error             { return errors.New("daemon: unsupported platform") }
func (d *unsupportedDaemon) Status() (string, error) { return "unknown", nil }
