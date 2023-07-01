plugin/rootfs: *.go
	docker build -t docker-volume-icloud --platform linux/amd64 .
	mkdir -p plugin/rootfs
	docker export "`docker create --platform linux/amd64 docker-volume-icloud true`" | tar -x -C plugin/rootfs

cheif/icloud: plugin/rootfs
	- docker plugin disable cheif/icloud
	- docker plugin rm cheif/icloud
	docker plugin create cheif/icloud plugin

publish: cheif/icloud
	docker plugin push cheif/icloud

install: cheif/icloud
	docker plugin set cheif/icloud ACCESS_TOKEN=$(ACCESS_TOKEN) WEBAUTH_USER=$(WEBAUTH_USER)
	docker plugin enable cheif/icloud

clean:
	rm -rf plugin/rootfs
