#!/usr/bin/env python3
"""Record a HAR trace of the Brother printer's certificate pages, for porting
brother-cert to a new firmware.

The script opens your own Chrome with recording on, at the printer's web UI.
You click through the steps it prints; it fills in the admin password the
first time the page asks for one, so you only type it here. When you press
Enter, it saves the trace with the password and every cookie replaced by
REDACTED, checks that the password appears nowhere in the file, and deletes
everything else it made. It also makes a throwaway certificate to import.

Usage: python3 capture-printer.py [--host mfc-l2900dw.lan] [--out printer-<date>.har]
Needs playwright and cryptography, e.g. in a virtual environment:
    python3 -m venv ~/.venvs/capture
    ~/.venvs/capture/bin/pip install playwright cryptography
    ~/.venvs/capture/bin/python capture-printer.py
It drives the installed Chrome, so no browser is downloaded, and Chrome
trusts the homelab root the way the Mac does.
"""
import argparse
import datetime
import getpass
import html
import json
import os
import secrets
import shutil
import sys
import tempfile
import urllib.parse

from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.hazmat.primitives.serialization import pkcs12
from playwright.sync_api import sync_playwright

STEPS = """
In the Chrome window that opened, without changing anything else:
  1. Log in (the password is filled in for you; just press Login).
  2. Open Network > Security > Certificate (the certificate list).
  3. Import the throwaway certificate: "Import Certificate and Private Key",
       file      {p12}
       password  {p12_password}
     and wait until it appears in the list.
  4. Open it in the list and delete it again.
  5. Open Network > Network > Protocol > HTTP Server Settings.
     Look, but do NOT press Submit (that switches certificates and
     restarts the printer).
  6. Log out.
Then come back here and press Enter.
"""


def make_p12(directory):
    """Make a throwaway self-signed RSA 2048 certificate for capture-test.lan
    (the printer accepts only RSA) as a PKCS#12 file in the given directory.
    Returns the file's path and its random password. The certificate is only
    imported and deleted again, never selected."""
    key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    name = x509.Name([x509.NameAttribute(x509.NameOID.COMMON_NAME, "capture-test.lan")])
    now = datetime.datetime.now(datetime.timezone.utc)
    cert = (
        x509.CertificateBuilder()
        .subject_name(name)
        .issuer_name(name)
        .public_key(key.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(now - datetime.timedelta(minutes=5))
        .not_valid_after(now + datetime.timedelta(days=1))
        .add_extension(x509.SubjectAlternativeName([x509.DNSName("capture-test.lan")]), critical=False)
        .sign(key, hashes.SHA256())
    )
    password = secrets.token_hex(6)
    data = pkcs12.serialize_key_and_certificates(
        b"capture-test", key, cert, None,
        serialization.BestAvailableEncryption(password.encode()))
    path = os.path.join(directory, "capture-test.p12")
    with open(path, "wb") as f:
        f.write(data)
    return path, password


def password_forms(password):
    """Return every form the password can take in a HAR file: as typed, URL
    encoded (form posts, query strings), JSON escaped and HTML escaped."""
    forms = {
        password,
        urllib.parse.quote(password, safe=""),
        urllib.parse.quote_plus(password),
        json.dumps(password)[1:-1],
        html.escape(password),
    }
    return sorted((f for f in forms if f), key=len, reverse=True)


def redact(har, password):
    """Replace the password, in each of its forms, with REDACTED in every
    string of a loaded HAR, and blank out all cookies (Cookie and Set-Cookie
    headers and the cookie lists). Returns the HAR, changed in place."""
    forms = password_forms(password)

    # Walk every value, rewriting strings
    def scrub(value):
        if isinstance(value, str):
            for form in forms:
                value = value.replace(form, "REDACTED")
            return value
        if isinstance(value, list):
            return [scrub(v) for v in value]
        if isinstance(value, dict):
            return {k: scrub(v) for k, v in value.items()}
        return value

    har = scrub(har)

    # Session cookies would let someone reuse the login while it lasts
    for entry in har["log"]["entries"]:
        for side in (entry["request"], entry["response"]):
            for header in side.get("headers", []):
                if header["name"].lower() in ("cookie", "set-cookie"):
                    header["value"] = "REDACTED"
            for cookie in side.get("cookies", []):
                cookie["value"] = "REDACTED"
    return har


def main():
    """Run the capture: ask for the password, open Chrome with recording on,
    wait for the user to click through the steps, then write the redacted
    trace. Exits non-zero, writing nothing, if the password would survive
    redaction."""
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--host", default="mfc-l2900dw.lan")
    parser.add_argument("--out", default=f"printer-{datetime.date.today()}.har")
    parser.add_argument("--browser", default="chrome", help="chrome (your Chrome) or chromium (Playwright's, for testing)")
    parser.add_argument("--headless", action="store_true", help=argparse.SUPPRESS)
    args = parser.parse_args()

    # The password, only to fill it in and to redact it; never written anywhere
    password = getpass.getpass(f"Admin password for {args.host} (used to log in and to redact it): ")
    if not password:
        sys.exit("no password given")

    # A private scratch directory for the throwaway certificate and the raw trace
    work = tempfile.mkdtemp(prefix="printer-capture-")
    os.chmod(work, 0o700)
    raw = os.path.join(work, "raw.har")
    try:
        p12, p12_password = make_p12(work)

        # Chrome, recording every request with its body
        with sync_playwright() as p:
            launch = {"headless": args.headless}
            if args.browser == "chrome":
                launch["channel"] = "chrome"
            browser = p.chromium.launch(**launch)
            context = browser.new_context(record_har_path=raw, record_har_content="embed")
            page = context.new_page()

            # Fill the password into the first password field any page shows
            # (the login form), once; the user still presses Login
            filled = {"done": False}

            def fill_password(frame):
                if filled["done"] or frame != page.main_frame:
                    return
                try:
                    field = page.locator("input[type=password]").first
                    if field.count():
                        field.fill(password)
                        filled["done"] = True
                except Exception:  # noqa: BLE001 - the page may be navigating
                    pass

            page.on("framenavigated", fill_password)
            page.goto(f"https://{args.host}/")
            fill_password(page.main_frame)

            # The user clicks through, then presses Enter here
            print(STEPS.format(p12=p12, p12_password=p12_password))
            input("Press Enter when done... ")
            context.close()
            browser.close()

        # Redact, and refuse to write the file if the password is still in it
        with open(raw) as f:
            har = redact(json.load(f), password)
        text = json.dumps(har, indent=1)
        if any(form in text for form in password_forms(password)):
            sys.exit("the password is still in the trace after redaction; nothing was written")
        fd = os.open(args.out, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w") as f:
            f.write(text)

        # What was captured
        entries = har["log"]["entries"]
        posts = [e["request"]["url"] for e in entries if e["request"]["method"] == "POST"]
        print(f"\nWrote {args.out}: {len(entries)} requests, {len(posts)} form posts, password and cookies redacted.")
        if not posts:
            print("No form posts were recorded; did the steps run in that window?")
    finally:
        # Nothing else stays behind
        shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    main()
