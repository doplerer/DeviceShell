package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func (app *Console) resetCommand() *cobra.Command {
	var yes bool
	c := &cobra.Command{Use: "reset", Short: app.Service.L("Borrar todos los datos de uso; conservar ajustes y rutinas", "Delete all usage data; keep settings and routines"), Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		app.Service.ReleaseUsage()
		if e := app.Service.AcquireUsage(true); e != nil {
			return e
		}
		r, e := app.Service.ResetPreview()
		if e != nil {
			return e
		}
		if !yes {
			if !app.InteractiveTerminal() {
				return errors.New("reset requires --yes without a terminal")
			}
			fmt.Fprintln(os.Stderr, app.Service.ResetDescription(r))
			fmt.Fprintln(os.Stderr, app.Service.L("Escribe BORRAR para confirmar:", "Type BORRAR to confirm:"))
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if strings.TrimSpace(line) != "BORRAR" {
				return errors.New(app.Service.L("Cancelado", "Canceled"))
			}
		}
		r, e = app.Service.EraseUsage()
		if e != nil {
			if app.jsonFlag {
				_ = app.output(r)
				return app.errBatchFailed
			}
			return e
		}
		return app.output(r)
	}}
	c.Flags().BoolVar(&yes, "yes", false, app.Service.L("Confirmar el borrado directo, sin papelera", "Confirm direct deletion, without the recycle bin"))
	return c
}
