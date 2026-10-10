#!/usr/bin/env bash
# Creates the self-signed certificate CI signs macOS builds with. Every build signed by it keeps
# the same designated requirement, so macOS permissions carry over to updates.
# Run once, then set the MACOS_CERTIFICATE and MACOS_CERTIFICATE_PASSWORD repository secrets.
# Never replace it: users would have to grant every permission again.
set -euo pipefail

out_dir="${1:-$HOME/encre-signing}"
name="Encre"

if [ -e "$out_dir" ]; then
  echo "Refusing to overwrite $out_dir: replacing the certificate resets every user's permissions." >&2
  exit 1
fi

mkdir -p "$out_dir"
chmod 700 "$out_dir"

key="$out_dir/signing.key"
cert="$out_dir/signing.cer"
p12="$out_dir/signing.p12"
password_file="$out_dir/macos-certificate-password.txt"
base64_file="$out_dir/macos-certificate.base64.txt"

openssl rand -base64 24 | tr -d '\n' >"$password_file"
password="$(cat "$password_file")"

# Without the codeSigning usage macOS reports "Missing required extension"; 20 years, as an expired one cannot sign.
openssl req -x509 -newkey rsa:4096 -sha256 -days 7300 -nodes \
  -keyout "$key" -out "$cert" \
  -subj "/CN=$name/O=$name" \
  -addext "basicConstraints=critical,CA:true" \
  -addext "keyUsage=critical,digitalSignature,keyCertSign" \
  -addext "extendedKeyUsage=critical,codeSigning" \
  2>/dev/null

# macOS `security import` cannot read OpenSSL 3's default PKCS#12 encryption.
openssl pkcs12 -export -legacy \
  -inkey "$key" -in "$cert" -out "$p12" \
  -name "$name" -passout "pass:$password"

base64 -w0 "$p12" >"$base64_file" 2>/dev/null || base64 -i "$p12" | tr -d '\n' >"$base64_file"

chmod 600 "$key" "$p12" "$password_file" "$base64_file"

openssl x509 -in "$cert" -noout -subject -enddate -fingerprint -sha256
echo
echo "Set these repository secrets:"
echo "  MACOS_CERTIFICATE           <- $base64_file"
echo "  MACOS_CERTIFICATE_PASSWORD  <- $password_file"
echo
echo "Back up $out_dir somewhere safe, outside git."
