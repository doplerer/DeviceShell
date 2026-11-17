package cli

import "github.com/spf13/cobra"

func (app *Console) addExtras(c *cobra.Command) {
	d := &cobra.Command{Use: "device", Short: app.Service.L("Gestionar placas", "Manage devices")}
	d.AddCommand(&cobra.Command{Use: "rename ALIAS_OR_SERIAL NEW_ALIAS", Short: app.Service.L("Cambiar el alias de una placa", "Change a device alias"), Args: cobra.ExactArgs(2), RunE: func(_ *cobra.Command, a []string) error {
		v, e := app.Service.RenameDevice(a[0], a[1])
		if e != nil {
			return e
		}
		return app.output(v)
	}})
	c.AddCommand(d)
	for _, g := range c.Commands() {
		if g.Name() == "group" {
			g.AddCommand(&cobra.Command{Use: "set NAME", Args: cobra.ExactArgs(1), Short: app.Service.L("Sustituir los miembros de un grupo", "Replace the members of a group"), RunE: func(_ *cobra.Command, a []string) error {
				d, e := app.Service.Discover()
				if e != nil {
					return e
				}
				target, e := app.selectTargets(d)
				if e != nil {
					return e
				}
				ids := []string{}
				for _, v := range target {
					ids = append(ids, v.Serial)
				}
				groups, e := app.Service.SetGroup(a[0], ids)
				if e != nil {
					return e
				}
				return app.output(groups)
			}})
		}
	}
}
