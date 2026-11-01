package printer

import (
	"errors"
	"fmt"
	"slices"
)

const urlCertImport = "/net/security/certificate/import.html"

// importFormID is the id of the form on the certificate import page
const importFormID = "cert_import"

// UploadNewCert converts the specified pem files into p12 format and installs them
// on the printer. It returns the id value of the newly installed cert.
func (p *printer) UploadNewCert(keyPem, certPem []byte) (string, error) {
	// make p12 from key and cert pem
	p12, err := makeModernPfx(keyPem, certPem, "")
	if err != nil {
		return "", fmt.Errorf("printer: failed to make p12 file (%w)", err)
	}

	// GET current cert IDs
	origCertIDs, err := p.getCertIDs()
	if err != nil {
		return "", err
	}

	// GET the import page and read its form
	u, err := p.pageURL(urlCertImport)
	if err != nil {
		return "", err
	}
	bodyBytes, err := p.getPage(u)
	if err != nil {
		return "", fmt.Errorf("printer: get of certificate import page failed (%w)", err)
	}
	form, err := parseForm(bodyBytes, importFormID, "")
	if err != nil {
		return "", fmt.Errorf("printer: upload: %w", err)
	}
	if len(form.fileInputs) != 1 {
		return "", fmt.Errorf("printer: upload: expected one file input on the import page, found %d", len(form.fileInputs))
	}

	// the p12 has no password: the password input and the hidden copy the
	// page's script makes of it (hidden_cert_import_password) stay empty
	for _, name := range form.passwordInputs {
		form.set(name, "")
	}
	form.set("hidden_cert_import_password", "")

	// submit the form with the p12 attached
	actionURL, err := form.actionURL(u)
	if err != nil {
		return "", err
	}
	bodyBytes, err = p.postMultipart(actionURL, form, form.fileInputs[0], "certkey.p12", p12)
	if err != nil {
		return "", fmt.Errorf("printer: post of new certificate failed (%w)", err)
	}

	// the printer answers with "Please wait" pages until the import is done
	finalPage, err := p.followWaitPages(bodyBytes, actionURL)
	if err != nil {
		return "", fmt.Errorf("printer: upload: %w", err)
	}

	// get new cert ID list
	newCertIDs, err := p.getCertIDs()
	if err != nil {
		return "", err
	}

	// find ID that is in new list but not in old (this is the new one)
	newId := ""
	countNew := 0
	for i := range newCertIDs {
		found := slices.Contains(origCertIDs, newCertIDs[i])

		if !found {
			newId = newCertIDs[i]
			countNew++
		}
	}

	// if more than one new, can't determine which was uploaded by this app
	if countNew > 1 {
		return "", errors.New("printer: upload: failed to deduce new cert's id")
	}
	if countNew == 0 {
		msg := pageMessages(finalPage)
		if msg == "" {
			msg = "no message"
		}
		return "", fmt.Errorf("printer: upload: the printer did not add the certificate (it said: %s)", msg)
	}

	return newId, nil
}
