#!/bin/sh
set -euo pipefail
sudo rc-service filex stop
git pull
sudo make install
sudo rc-service filex start
