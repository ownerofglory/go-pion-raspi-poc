package media

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

func StartGst(ctx context.Context, pipeline string, tag string) *exec.Cmd {
	slog.Info("Started gst-launch", "tag", tag)

	// split command: gst-launch-1.0 <elements...>
	args := append([]string{"-e"}, strings.Fields(pipeline)...)
	cmd := exec.CommandContext(ctx, "gst-launch-1.0", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		slog.Error("Failed to start gst-launch", "err", err)
		return nil
	}
	slog.Info("Started gst-launch", "args", cmd.Args)
	return cmd
}
