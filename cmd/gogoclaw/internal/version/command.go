package version

import (
	"github.com/spf13/cobra"

	"github.com/biaobiaobiao108/gogoclaw/cmd/gogoclaw/internal"
	"github.com/biaobiaobiao108/gogoclaw/cmd/gogoclaw/internal/cliui"
	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
)

func NewVersionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "version",
		Aliases: []string{"v"},
		Short:   "Show version information",
		Run: func(_ *cobra.Command, _ []string) {
			printVersion()
		},
	}

	return cmd
}

func printVersion() {
	build, goVer := config.FormatBuildInfo()
	cliui.PrintVersion(internal.Logo, "gogoclaw "+config.FormatVersion(), build, goVer)
}
