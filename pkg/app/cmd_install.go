package app

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/gregtwallace/brother-cert/pkg/printer"
)

// reconnectAttempts and reconnectDelay are how often, and how far apart, the
// login is retried after the printer restarts
const (
	reconnectAttempts = 8
	reconnectDelay    = 15 * time.Second
)

// cmdInstallCertAndReset executes a series of commands against a brother printer
// to install the specified ssl key and cert. it then deletes the old cert and
// resets the printer so it will load the newly installed key/cert
func (app *app) cmdInstallCertAndReset(_ context.Context, args []string) error {
	// extra args == error
	if len(args) != 0 {
		return fmt.Errorf("main: failed, %w (%d)", ErrExtraArgs, len(args))
	}

	// must have hostname and password
	if app.config.hostname == nil || *app.config.hostname == "" {
		return errors.New("main: hostname must be specified")
	}
	if app.config.password == nil || *app.config.password == "" {
		return errors.New("main: hostname must be specified")
	}

	// use http?
	useHttp := false
	if app.config.http != nil && *app.config.http {
		app.stdLogger.Println("WARNING: --http flag set, insecure http connection will be used")
		useHttp = true
	}

	// insecure https?
	insecureHTTPS := false
	if app.config.insecure != nil && *app.config.insecure {
		app.stdLogger.Println("WARNING: --insecure flag set, printer https connection certificate will not be verified")
		insecureHTTPS = true
	}

	// load key and cert
	keyPem, certPem, err := app.config.keyCertPemCfg.GetPemBytes("main")
	if err != nil {
		return err
	}

	// make printer (which includes login)
	printerCfg := printer.Config{
		Hostname:      *app.config.hostname,
		Password:      *app.config.password,
		UseHttp:       useHttp,
		UserAgent:     fmt.Sprintf("brother-cert/%s (%s; %s)", appVersion, runtime.GOOS, runtime.GOARCH),
		InsecureHTTPS: insecureHTTPS,
	}

	print, err := printer.NewPrinter(printerCfg)
	if err != nil {
		return err
	}
	app.stdLogger.Println("main: connected to printer")

	// if using https, check if the cert we're trying to install is already in use
	if !useHttp {
		app.stdLogger.Println("main: checking current printer cert ...")
		currCert, err := print.GetCurrentLeafCert()
		if err != nil {
			return err
		}

		// decode leaf cert
		newCertPemBlock, _ := pem.Decode(certPem)
		if newCertPemBlock == nil {
			return errors.New("main: failed to decode new leaf cert pem block")
		}

		// parse 1st cert
		newCert, err := x509.ParseCertificate(newCertPemBlock.Bytes)
		if err != nil {
			return fmt.Errorf("failed to parse new leaf certificate (%s)", err)
		}

		if bytes.Equal(currCert.SerialNumber.Bytes(), newCert.SerialNumber.Bytes()) {
			app.stdLogger.Println("main: current printer certificate and new certificate to upload are the same, aborting")
			return nil
		}
	} else {
		app.stdLogger.Println("main: skipping check of current printer cert (--http flag was set)")
	}

	// get current ssl cert id
	oldCertId, oldCertName, err := print.GetCurrentCertID()
	if err != nil {
		return err
	}
	app.stdLogger.Printf("main: current printer cert is %s (id: %s)", oldCertName, oldCertId)

	// install new key/cert
	app.stdLogger.Println("main: uploading new cert...")
	newCertId, err := print.UploadNewCert(keyPem, certPem)
	if err != nil {
		return err
	}
	app.stdLogger.Printf("main: new printer cert installed (but not yet activated) (id: %s)", newCertId)

	// activate new key/cert
	app.stdLogger.Printf("main: activating cert (id: %s) and rebooting... please wait 60 seconds...", newCertId)
	err = print.SetActiveCert(newCertId)
	if err != nil {
		return err
	}

	// IF deleting old cert (i.e. old id != 0 (0 cant be deleted, its "Preset"))
	if oldCertId != "0" {
		// wait for reboot to finish
		time.Sleep(60 * time.Second)
		app.stdLogger.Printf("main: reboot should be complete")

		// use https now (even if user originally said not to, since cert is installed)
		printerCfg.UseHttp = false

		// must login again due to the restart (the printer may take a while
		// longer to answer)
		for attempt := 1; ; attempt++ {
			print, err = printer.NewPrinter(printerCfg)
			if err == nil {
				break
			}
			if attempt == reconnectAttempts {
				return fmt.Errorf("main: failed to reconnect to printer (%w)", err)
			}
			app.stdLogger.Printf("main: printer not back yet (%s), retrying in %s", err, reconnectDelay)
			time.Sleep(reconnectDelay)
		}
		app.stdLogger.Println("main: reconnected to printer")

		// only delete the old cert once the new one is in use, so a failed
		// switch never leaves the printer without its working cert
		activeId, _, err := print.GetCurrentCertID()
		if err != nil {
			return fmt.Errorf("main: failed to check the active cert after restart, not deleting the old one (%w)", err)
		}
		if activeId != newCertId {
			return fmt.Errorf("main: after restart the active cert is %s, not the new %s; not deleting the old one", activeId, newCertId)
		}

		// do delete of old cert
		app.stdLogger.Printf("main: deleting old cert (id: %s) ...", oldCertId)
		err = print.DeleteCert(oldCertId)
		if err != nil {
			return fmt.Errorf("main: failed to delete cert (id: %s) (%w)", oldCertId, err)
		}

		app.stdLogger.Printf("main: old cert (id: %s) deleted", oldCertId)
	}

	return nil
}
