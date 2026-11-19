package auth

import "github.com/spf13/cobra"

func NewAuthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate chat channels",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newWeixinCommand())

	return cmd
}
