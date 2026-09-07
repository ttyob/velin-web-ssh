# Velin Web SSH for fnOS

This directory is a native fnOS application package source. The package runs
the Velin Go binary and guacd as native processes. Docker is not required on
the NAS.

Build it from the repository root:

```sh
packaging/fnos/build.sh
```

Build a package for the current host architecture:

```sh
VELIN_FNOS_VERSION=0.3.27 packaging/fnos/build.sh
```

The release workflow builds separate `amd64` and `arm64` packages. The build
machine needs Docker to extract the matching guacd runtime files, but Docker is
not needed after installation. Install the matching `.fpk` through fnOS App
Center. ffmpeg is optional and uses the NAS `ffmpeg` command when recordings
are enabled.

Reset a forgotten administrator password over SSH:

```sh
sudo -u velin env \
  TRIM_APPDEST=/var/apps/velin-web-ssh/target \
  TRIM_PKGVAR=/var/apps/velin-web-ssh/var \
  /var/apps/velin-web-ssh/target/cmd/main reset-admin-password
```

Set `VELIN_RESET_PASSWORD` in the command environment to choose the new
password instead of generating a temporary one. The command restores the
service to its previous running state after the reset.
