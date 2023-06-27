plugin/rootfs: *.go
	docker build -t docker-volume-icloud --platform linux/amd64 .
	mkdir -p plugin/rootfs
	docker export "`docker create --platform linux/amd64 docker-volume-icloud true`" | tar -x -C plugin/rootfs

berglund.io/icloud: plugin/rootfs
	- docker plugin disable berglund.io/icloud
	- docker plugin rm berglund.io/icloud
	docker plugin create berglund.io/icloud plugin


install: berglund.io/icloud
	docker plugin set berglund.io/icloud ACCESS_TOKEN=$(ACCESS_TOKEN) WEBAUTH_USER=$(WEBAUTH_USER)
	docker plugin enable berglund.io/icloud

clean:
	rm -rf plugin/rootfs
