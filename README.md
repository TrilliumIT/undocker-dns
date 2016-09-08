In Docker 1.10 docker [took control](https://github.com/docker/docker/issues/19474) of your container's DNS and refused to provide an option to disable this functionality.

This little daemon gives back that control that docker doesn't want you to have. It uses inotify to watch for any new container resolv.conf files in `/var/lib/docker/containers/*/resolv.conf` and keeps them in sync with your host's `/etc/resolv.conf`.

To install, just download the latest binary from the releases page and put it in /usr/local/bin/ and install and activate the systemd unit file.
