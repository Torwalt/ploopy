package ui

import (
	"fmt"
	"io"
)

func PoweroffArmed(out io.Writer) {
	fmt.Fprintln(out, "Shutdown watcher armed: powers off once no plan runs and you are away; disarms if you are here when they finish.")
	fmt.Fprintln(out, "Cancel with `ploopy poweroff stop`; inspect with `systemctl --user status ploopy-poweroff.service`.")
}
