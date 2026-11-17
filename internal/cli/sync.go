package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/doplerer/deviceshell/internal/core"
)

func (app *Console) syncCommand() *cobra.Command {
	s := &cobra.Command{Use: "sync", Short: app.Service.L("Replica en vivo mediante scrcpy", "Live replica through scrcpy")}
	var master, views string
	start := &cobra.Command{Use: "start", Short: app.Service.L("Iniciar sesion (Ctrl+C para terminar)", "Start a session (Ctrl+C to end)"), RunE: func(*cobra.Command, []string) error {
		return app.startSync(master, views)
	}}
	start.Flags().StringVar(&master, "master", "", app.Service.L("Alias o serie principal (debe estar en la seleccion)", "Master alias or serial (must be in the selection)"))
	start.Flags().StringVar(&views, "views", "all", app.Service.L("all: principal y seguidores; master: solo principal", "all: master and followers; master: master only"))
	start.Flags().IntVar(&app.Service.MasterSize, "master-size", 1024, app.Service.L("Resolucion maxima principal", "Master maximum resolution"))
	start.Flags().IntVar(&app.Service.MasterFPS, "master-fps", 60, app.Service.L("FPS principal", "Master FPS"))
	start.Flags().IntVar(&app.Service.FollowerSize, "follower-size", 480, app.Service.L("Resolucion maxima seguidores", "Followers maximum resolution"))
	start.Flags().IntVar(&app.Service.FollowerFPS, "follower-fps", 30, app.Service.L("FPS seguidores", "Followers FPS"))
	s.AddCommand(start)
	for _, action := range []string{"status", "pause", "resume", "stop"} {
		a := action
		s.AddCommand(&cobra.Command{Use: a + " ID", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
			b, e := app.Service.SyncRequest(args[0], a)
			if e != nil {
				return e
			}
			fmt.Println(b)
			return nil
		}})
	}
	return s
}
func (app *Console) startSync(master, views string) error {
	if views != "all" && views != "master" {
		return errors.New(app.Service.L("--views debe ser all o master", "--views must be all or master"))
	}
	d, e := app.Service.Discover()
	if e != nil {
		return e
	}
	targets, e := app.selectTargets(d)
	if e != nil {
		return e
	}
	var m core.Device
	for _, v := range targets {
		if v.Serial == master || strings.EqualFold(v.Alias, master) {
			m = v
		}
	}
	if m.Serial == "" || m.State != "device" {
		return errors.New(app.Service.L("La principal debe estar conectada y formar parte de la seleccion", "The master must be connected and part of the selection"))
	}
	o := core.SyncOptions{Master: m, Targets: targets, Views: views, MasterSize: app.Service.MasterSize, MasterFPS: app.Service.MasterFPS, FollowerSize: app.Service.FollowerSize, FollowerFPS: app.Service.FollowerFPS}
	return app.Service.RunSyncSession(app.Service.Ctx, o, nil, func(s *core.LiveSession) {
		if app.jsonFlag {
			json.NewEncoder(os.Stdout).Encode(s.Info())
		} else {
			fmt.Fprintf(os.Stderr, app.Service.L("Sesion %s. Principal %s, %d seguidores. Usa sync pause/resume/status/stop ID o Ctrl+C.\n", "Session %s. Master %s, %d followers. Use sync pause/resume/status/stop ID or Ctrl+C.\n"), s.ID, m.Alias, len(s.Peers))
		}
	})
}
