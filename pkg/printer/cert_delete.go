package printer

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
)

const urlCertDelete = "/net/security/certificate/delete.html"

// deleteFormID is the id of the form on the certificate delete page
const deleteFormID = "cert_delete"

var errCertDeleteInvalidID = errors.New("printer: cant delete cert (invalid id)")

// DeleteCert deletes the certificate with the specified ID from the
// printer
func (p *printer) DeleteCert(id string) error {
	// verify ID actually exists and isn't 0 ('Preset') which isn't valid
	if len(id) <= 0 || id == "0" {
		return errCertDeleteInvalidID
	}

	existingIDs, err := p.getCertIDs()
	if err != nil {
		return err
	}

	validID := slices.Contains(existingIDs, id)
	if !validID {
		return errCertDeleteInvalidID
	}

	// GET the delete page for this certificate and read its form
	u, err := p.pageURL(urlCertDelete + "?idx=" + url.QueryEscape(id))
	if err != nil {
		return err
	}
	bodyBytes, err := p.getPage(u)
	if err != nil {
		return fmt.Errorf("printer: get of delete page failed (%w)", err)
	}
	form, err := parseForm(bodyBytes, deleteFormID, "")
	if err != nil {
		return fmt.Errorf("printer: delete: %w", err)
	}

	// it must be the form for this certificate
	if formID, _ := form.get("hidden_certificate_idx"); formID != id {
		return fmt.Errorf("printer: delete: the delete page is for certificate %q, not %q", formID, id)
	}

	// submit it (the page's Submit button does nothing else)
	actionURL, err := form.actionURL(u)
	if err != nil {
		return err
	}
	bodyBytes, err = p.postForm(actionURL, u, form.values())
	if err != nil {
		return fmt.Errorf("printer: delete: %w", err)
	}

	// the printer answers with "Please wait" pages until the delete is done
	if _, err = p.followWaitPages(bodyBytes, actionURL); err != nil {
		return fmt.Errorf("printer: delete: %w", err)
	}

	// check id list and ensure its gone
	existingIDs, err = p.getCertIDs()
	if err != nil {
		return err
	}

	idFound := slices.Contains(existingIDs, id)
	if idFound {
		return errors.New("printer: failed to delete cert (still exists)")
	}

	return nil
}
