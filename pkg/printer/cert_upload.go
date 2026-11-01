package printer

import (
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"slices"
)

const urlCertImport = "/net/security/certificate/import.html"

// importFormID is the id of the form on the certificate import page
const importFormID = "cert_import"

// importLinkRegex finds the certificate list's link to the import page,
// e.g. <a href="import.html?pageid=477" >
var importLinkRegex = regexp.MustCompile(`<a[^>]+href="(import\.html[^"]*)"`)

// certListLink returns the certificate list's link matching the regex (its
// first capture), resolved against the list page; if the list has none, the
// fallback path
func (p *printer) certListLink(link *regexp.Regexp, fallback string) (*url.URL, error) {
	listURL, err := p.pageURL(urlCertList)
	if err != nil {
		return nil, err
	}
	body, err := p.getPage(listURL)
	if err != nil {
		return nil, err
	}
	if caps := link.FindSubmatch(body); len(caps) == 2 {
		return listURL.Parse(html.UnescapeString(string(caps[1])))
	}
	return p.pageURL(fallback)
}

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

	// GET the import page (through the certificate list's link to it, as a
	// browser does) and read its form
	u, err := p.certListLink(importLinkRegex, urlCertImport)
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
	bodyBytes, err = p.postMultipart(actionURL, u, form, form.fileInputs[0], "certkey.p12", p12)
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
