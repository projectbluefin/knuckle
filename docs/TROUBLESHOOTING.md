# Troubleshooting

Common failure modes for knuckle installs and first boot.

## 0) Quick data to collect before rebooting

Run these from the installer shell:

```bash
knuckle --version
lsblk -o NAME,SIZE,TYPE,MODEL,SERIAL
ip -br a
cat /tmp/knuckle.log
```

If you are in a booted installed system, collect:

```bash
cat /etc/os-release
journalctl -u sshd -b --no-pager | tail -100
journalctl -u systemd-networkd -b --no-pager | tail -100
journalctl -b --no-pager | tail -200
```

## 1) Installer hangs or does not start

### Symptom

- USB boots, but the installer never appears.
- Boot stalls around disk/GPT probing.
- Boot drops to an emergency shell after 10-15 seconds.

### Checks and fixes

1. **Use UEFI mode** (knuckle ISO is systemd-boot, UEFI-only).
2. Re-write the USB in raw/DD mode.
3. At the systemd-boot menu, press `e` and ensure the kernel command line includes:

```text
systemd.gpt_auto=0 rd.driver.pre=loop
```

Knuckle's ISO entries already include both flags; adding them manually helps when firmware overrides boot options.
If your hardware only supports legacy BIOS/CSM, use the upstream Flatcar installation path instead of knuckle's ISO.

## 2) Target disk missing in Storage step

### Symptom

- Storage step is empty, or the expected target disk is missing.

### Checks and fixes

```bash
lsblk -o NAME,SIZE,TYPE,MODEL,SERIAL
ls -l /dev/disk/by-id/
```

- Use `/dev/disk/by-id/...` disk paths whenever available (stable identity).
- Avoid relying on `/dev/sdX` ordering.
- In some VM environments, `/dev/disk/by-id/` can be empty for virtio disks with no serials. In that case, use the visible `/dev/vdX` path for that environment only.
- For unattended installs, validate config first:

```bash
knuckle --headless --config install.json --dry-run
```

See [HEADLESS-CONFIG.md](HEADLESS-CONFIG.md) for the schema.

## 3) Install step fails (`flatcar-install` error)

### Symptom

- Install step exits with an error.
- Wizard reports failure before reboot.

### Checks and fixes

1. Review knuckle log:

```bash
cat /tmp/knuckle.log
```

2. Validate your config without writing disk:

```bash
knuckle --headless --config install.json --dry-run
```

3. For local VM reproduction, use repository recipes:

```bash
just vm
just vm-e2e
```

## 4) First boot: SSH login fails

### Symptom

- `ssh core@<ip>` returns `Permission denied` or `Connection refused`.

### Checks and fixes (from local console on installed system)

```bash
hostname
ip -br a
systemctl status sshd --no-pager
journalctl -u sshd -b --no-pager | tail -100
ls -lah /home/core/.ssh/authorized_keys
```

- Default user is `core` unless you configured another user.
- If key auth fails, verify the expected public key is present in `authorized_keys`.

## 5) First boot: network is down or wrong

### Checks (from installed system console)

```bash
networkctl status --no-pager
journalctl -u systemd-networkd -b --no-pager | tail -120
ls -lah /etc/systemd/network/
```

- Re-check static config values (interface, CIDR, gateway, DNS) if not using DHCP.

## 6) First boot: Ignition settings missing

### Symptom

- Hostname, SSH keys, or other expected config did not apply.

### Checks

```bash
hostname
journalctl -b -u 'ignition*' --no-pager | tail -200
ls -lah /home/core/.ssh/authorized_keys
```

If generation failed before install, rerun with `--dry-run` and inspect `/tmp/knuckle.log`.

## 7) VM/ghost lab constraints (contributors)

Use these when troubleshooting CI-style test environments:

- `just vm` / `just e2e` require a local display.
- Ghost is headless; use `just vm-e2e` or `./scripts/qa-test-pr.sh <PR>`.
- QEMU hostfwd on ghost binds `127.0.0.1` on ghost, so SSH to VM must be issued from ghost:

```bash
ssh jorge@ghost "ssh -o StrictHostKeyChecking=no -p 2307 core@127.0.0.1 'uname -r'"
```

- Known limitation: non-interactive ghost sessions can fail TUI tests with `open /dev/tty: no such device or address` (see issue [#512](https://github.com/projectbluefin/knuckle/issues/512)).

## 8) Recovering a host node accidentally provisioned with a knuckle Ignition config

If a bare-metal k3s node was previously provisioned with a knuckle Ignition config that included the swap feature, it may have `knuckle-create-swapfile.service` and `var-swapfile.swap` written into `/etc/systemd/system/` on the host. These units belong only inside ephemeral KubeVirt test VMs.

Symptoms: `var-swapfile.swap` fails at boot (because `knuckle-create-swapfile.service` is absent or already ran), and `/var/swapfile` occupies disk space.

Recovery (run from a privileged shell or via `kubectl debug node/<node>`):

```bash
systemctl stop var-swapfile.swap || true
systemctl disable var-swapfile.swap || true
rm -f /etc/systemd/system/var-swapfile.swap
rm -f /etc/systemd/system/multi-user.target.wants/var-swapfile.swap
rm -f /etc/systemd/system/knuckle-create-swapfile.service
rm -f /var/swapfile
systemctl daemon-reload
```

Verify with:
```bash
find /etc/systemd/system/ -name '*knuckle*' -o -name '*swap*'  # expect empty
swapon --show                                                    # expect empty
```

## 9) Still stuck?

Open an issue with:

- knuckle version (`knuckle --version`)
- target architecture (`uname -m`)
- whether install was interactive wizard or headless JSON
- relevant output from `/tmp/knuckle.log`
- first-boot journal excerpts (`journalctl -b --no-pager | tail -200`)
