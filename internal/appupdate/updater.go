package appupdate

import (
	"context"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
)

// Set only for production builds, alongside MyGo's update feed and public key.
var TeamID string

// Configure registers the native update window before App.Run. Installing or
// relaunching uses MyGo's normal quit sequence, including draft-save vetoes.
func Configure(open func(updater.Session)) {
	mygo.Updater.SetValidator(func(ctx context.Context, bundle, version string) error { return Validate(ctx, bundle, version, TeamID) })
	mygo.Use(updater.NewWithWindow(updater.Options{Language: "zh-Hans"}, open))
}
