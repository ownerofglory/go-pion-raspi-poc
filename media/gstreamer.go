package media

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

func StartGst(ctx context.Context, pipeline string, tag string) *exec.Cmd {
	slog.Info("Started gst-launch", "tag", tag, "args")

	// split command: gst-launch-1.0 <elements...>
	args := append([]string{"-e"}, strings.Fields(pipeline)...)
	cmd := exec.CommandContext(ctx, "gst-launch-1.0", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		slog.Error("Failed to start gst-launch", cmd.Args, err)
		return nil
	}
	slog.Info("Started gst-launch", cmd.Args)
	return cmd
}
