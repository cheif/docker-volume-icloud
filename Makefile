plugin/rootfs: *.go */**.go
	docker build -t docker-volume-icloud --platform linux/arm64 .
	mkdir -p plugin/rootfs
	docker export "`docker create --platform linux/arm64 docker-volume-icloud true`" | tar -x -C plugin/rootfs

cheif/icloud: plugin/rootfs
	- docker plugin rm -f cheif/icloud
	docker plugin create cheif/icloud plugin

publish: cheif/icloud
	docker plugin push cheif/icloud

install: cheif/icloud
	docker plugin set cheif/icloud DEBUG=$(DEBUG)
	docker plugin enable cheif/icloud

# Fire up a test-environment
testenv:
	docker build --target test-environment -t test-environment .
	docker run --device /dev/fuse --privileged -it --rm -p 5001:5000 -v `pwd`:/go/src/github.com/cheif/docker-volume-icloud -v icloud-state:/mnt/state \
		test-environment sh

clean:
	rm -rf plugin/rootfs
