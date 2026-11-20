# Brother Cert Changelog

## [v0.4.3] - 2026-11-20

Log in the way a browser does, from the printer's start page: firmware 1.10
of the MFC-L2900DW shows its login form on /home/status.html and refuses a
login that names /general/status.html, the page 0.4.2 started from (the
older page is still tried when the start page has no form). Tested against
pages captured from firmware Main 1.10, Sub1 1.11, whose CSRF tokens now
carry line breaks; they are sent back unchanged. Adds
`tools/capture-printer.py`, which records a redacted trace of the
printer's certificate pages for porting to a new firmware.


## [v0.4.2] - 2026-11-01

Send the Referer and Origin headers a browser sends with every form, and
open the import page through the certificate list's link: the MFC-L2900DW
refuses forms without them ("Submit Error"). The printer's "Submit Error"
notice is now reported when an import fails.


## [v0.4.1] - 2026-11-01

Send the certificate upload's parts in page order, as a browser does: the
MFC-L2900DW silently ignores an upload whose file part comes last. When an
import is refused, report the printer's own message.


## [v0.4.0] - 2026-11-01

Support for the MFC-L2900DW (firmware Main 1.08, Sub1 1.11): forms are
read from the printer's pages instead of hard-coded, and the old
certificate is deleted only once the new one is in use.


## [v0.3.2] - 2026-05-21

Add `--insecure` flag to disable https certificate checks. This should
not generally be used, particularly in automation.

Also some dependency updates.


## [v0.3.1] - 2026-05-21

Dependency and build process updates.


## [v0.3.0] - 2025-09-09

Many items were changed or overhauled in this version. Possibly the
most important change comes from @wesleykirkland. The app now scans
the printer's login page to identify the correct field name for the
login password field. This should greatly expand compatibility with
other printer models. Other field names appear not to change with
different models, so hopefully this one change is enough to bring
wider support for many.

- Add universal login scan to detect the correct name of the password
  field for printer login.
- Add workaround when a certificate has no Common Name. This is
  considered a more "modern" standard (e.g., the Let's Encrypt
  `tlsserver` profile) and is now supported.
- Add check to see if the specified certificate is already in use
  by the printer. If so, abort the install.
- Modify various regex patterns to make them more robust.
- Fix pem parsing function. The logic was not correct for files
  that didn't contain a 2nd certificate as part of the chain.
- Ensure the correct CSRF Token is always selected by the relevant
  regex.
- Change cert id into a string (instead of int). This eliminates
  unneeded type conversions and also is more flexible if some models
  use strings for their IDs instead of just numbers.
- Improve a number of error messages.
- Move User-Agent specification into a custom transport.
- Simplify build system and make it OS agnostic.
- Update all dependencies, including Go 1.25.1.
- Update README with more details about the application, usage, and
  how to build.


## [v0.2.1] - 2024-03-06

Update to Go 1.22.1, which includes some security fixes.


## [v0.2.0] - 2024-02-07

Initial release.
