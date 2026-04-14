sudo rc-service filex.openrc stop
git pull
make build
sudo  make install
sudo rc-service filex.openrc start
