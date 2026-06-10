#!/usr/bin/env bash
# Fetch the gitignored data assets basanite needs:
#   - Princeton WordNet 3.0 database files      -> <data>/dict/        (~35 MB unpacked)
#   - WordNet-InfoContent tables (SemCor IC)    -> <data>/wordnet_ic/  (~30 MB unpacked)
#   - GloVe 6B 100d vectors                     -> <data>/vectors/     (822 MB download, 347 MB kept)
#
# Requires: curl, tar, unzip. Needs ~1.2 GB transient disk during the GloVe step.
# Every artifact is verified against a pinned sha256 before use.
#
# Usage: scripts/fetch-data.sh [data-dir]   (default: ./data; the binary also
# checks $BASANITE_DATA and ~/.local/share/basanite — pass that path here for
# an install that works from any directory)
set -euo pipefail

WNDB_SHA256=658b1ba191f5f98c2e9bae3e25c186013158f30ef779f191d2a44e5d25046dc8
IC_SHA256=a931b34bb9013ac3c1291f64c812fd039802995a2b1246b8f7525e82080110e3
GLOVE_100D_SHA256=be4367dd257eb945217234f16307c5c74236b648a222cc0b4ffd0dda6a3350b6

for tool in curl tar unzip; do
  command -v "$tool" >/dev/null || { echo "error: $tool is required" >&2; exit 1; }
done

verify() { # verify <file> <sha256>
  echo "$2  $1" | sha256sum -c - >/dev/null || {
    echo "error: checksum mismatch for $1 — refusing to use it" >&2
    exit 1
  }
}

dest="${1:-data}"
mkdir -p "$dest"
cd "$dest"

if [ ! -d dict ]; then
  echo "fetching WordNet 3.0 database (~10 MB)..."
  curl -sSfL -O https://wordnetcode.princeton.edu/3.0/WNdb-3.0.tar.gz
  verify WNdb-3.0.tar.gz "$WNDB_SHA256"
  tar xzf WNdb-3.0.tar.gz
  rm WNdb-3.0.tar.gz
else
  echo "dict/ already present, skipping"
fi

if [ ! -d wordnet_ic ]; then
  echo "fetching WordNet IC tables (~10 MB, nltk_data mirror)..."
  curl -sSfL -o wordnet_ic.zip \
    https://raw.githubusercontent.com/nltk/nltk_data/gh-pages/packages/corpora/wordnet_ic.zip
  verify wordnet_ic.zip "$IC_SHA256"
  unzip -q wordnet_ic.zip
  rm wordnet_ic.zip
else
  echo "wordnet_ic/ already present, skipping"
fi

if [ ! -f vectors/glove.6B.100d.txt ]; then
  echo "fetching GloVe 6B vectors (822 MB — this is the slow one)..."
  curl -SfL --progress-bar -o glove.6B.zip \
    https://huggingface.co/stanfordnlp/glove/resolve/main/glove.6B.zip
  unzip -o -q glove.6B.zip glove.6B.100d.txt
  rm glove.6B.zip
  verify glove.6B.100d.txt "$GLOVE_100D_SHA256"
  mkdir -p vectors
  mv glove.6B.100d.txt vectors/
else
  echo "vectors/ already present, skipping"
fi

echo "done: $(pwd)"
