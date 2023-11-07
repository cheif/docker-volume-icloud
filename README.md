# docker-volume-icloud
This package provides a docker [volume plugin](https://docs.docker.com/engine/extend/plugins_volume/) that enables you to mount a folder from iCloud Drive as a volume in a docker container.

## Usage
Right now the usage/configuration of the plugin is pretty bare-bones, and requires you to first create a session, by running:
```sh
go run . --create-session --username <ICLOUD_USERNAME> --password <ICLOUD_PASSWORD> > session.json
```

This session-file then needs to be provided to the plugin, typically by copying it to `/var/run/docker/plugins`.

### Docker Desktop for Mac
Since docker runs in a virtual machine on Mac you need a workaround to share the file, using something like [this](https://github.com/rclone/rclone/issues/6981)

### Installing
Once the session-file is in place you can install the plugin, this should automatically find the credentials and use them:
```sh
docker plugin install cheif/icloud
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
Since this relies on generating a session using the same mechanics as icloud.com, I assume it will stop working after some time, and then you'd have to re-generate the session. While writing this I haven't had any problems yet, but I've only run it for ~1 week, so it'll probably break going forward.

# TODO
- [x] It seems like files aren't properly updated when writing to them, this probably stems from the fact that iCloud will just create a new file, and update the pointer of the node to the new one, and we're not picking this up properly. We probably need to invalidate the reference to this node I guess?
