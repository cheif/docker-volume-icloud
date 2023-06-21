plugin/rootfs: *.go
	docker build -t docker-volume-icloud .
	docker export "`docker create docker-volume-icloud true`" | tar -x -C plugin/rootfs

install: plugin/rootfs
	- docker plugin disable icloud
	- docker plugin rm icloud
	docker plugin create icloud plugin
	docker plugin set icloud ACCESS_TOKEN=$(ACCESS_TOKEN) WEBAUTH_USER=$(WEBAUTH_USER)
	docker plugin enable icloud
