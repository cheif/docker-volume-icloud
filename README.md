# docker-volume-icloud
This package provides a docker [volume plugin](https://docs.docker.com/engine/extend/plugins_volume/) that enables you to mount a folder from iCloud Drive as a volume in a docker container.

## Usage
Right now the usage/configuration of the plugin is pretty bare-bones, and requires you to log in to [iCloud](https://icloud.com) and copy two cookies, `X-APPLE-WEBAUTH-TOKEN` and `X-APPLE-WEBAUTH-USER`. These can be found using developer tools in the browser, searching for cookies.

### Installing
Once these are found you can install the plugin and provide the credentials by running:
```sh
docker plugin install cheif/icloud ACCESS_TOKEN=$(X-APPLE-WEBAUTH-TOKEN) WEBAUTH-USER=$(X-APPLE-WEBAUTH-USER)
```

### Creating a volume
When the plugin is installed you should be able to create a volume running something like:
```sh
docker volume create -d cheif/icloud --name icloud-volume -o path=/Documents
```

### Attaching volume to container
Then testing this in busybox:
```sh
docker run --rm -it -v icloud-volume:/mnt busybox ls /mnt
```

This should list the contents of your choosen path in iCloud drive (e.g. `Documents` in the above example).

### Where to go next
When all of this is done, you should be able to use this plugin to run whatever you like in docker, backed by storage from iCloud.

## Caveats
Since this relies on manually getting cookies from icloud.com, I assume it will stop working after some time, and then you'd have to fetch new cookies and update them. While writing this I haven't had any problems yet, but I've only run it for ~1 week, so it'll probably break going forward.

# TODO
- [] It seems like files aren't properly updated when writing to them, this probably stems from the fact that iCloud will just create a new file, and update the pointer of the node to the new one, and we're not picking this up properly. We probably need to invalidate the reference to this node I guess?
