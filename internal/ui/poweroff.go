package ui

import (
	"fmt"
	"io"
)

func PoweroffArmed(out io.Writer) {
	fmt.Fprintln(out, "Shutdown watcher armed: checks every 60 seconds and powers off when no plans are running.")
	fmt.Fprintln(out, "Cancel with `ploopy poweroff stop`; inspect with `systemctl status ploopy-poweroff.service`.")
}
