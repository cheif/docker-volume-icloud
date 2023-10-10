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
	docker plugin set cheif/icloud ACCESS_TOKEN=$(ACCESS_TOKEN) WEBAUTH_USER=$(WEBAUTH_USER) DEBUG=$(DEBUG)
	docker plugin enable cheif/icloud

# Fire up a test-environment
testenv:
	docker build --target test-environment -t test-environment .
	docker run --device /dev/fuse --privileged -it --rm -v `pwd`:/go/src/github.com/cheif/docker-volume-icloud \
		-e ACCESS_TOKEN=$(ACCESS_TOKEN) \
		-e WEBAUTH_USER=$(WEBAUTH_USER) \
		test-environment sh

clean:
	rm -rf plugin/rootfs
