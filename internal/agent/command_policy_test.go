package agent

import "testing"

func TestCommandRequiresApproval(t *testing.T) {
	largestFiles := `df -hT && printf '\nLargest files on / (top 30):\n' && find / -xdev -type f -printf '%s\t%p\n' 2>/dev/null | sort -nr | head -30 | awk -F '\t' '{printf "%.1f GiB\t%s\n", $1/1024/1024/1024, $2}'`
	tests := map[string]bool{
		largestFiles:                       false,
		"df -h":                            false,
		"ps aux | head -20":                false,
		"docker ps --format '{{.Names}}'":  false,
		"git status --short":               false,
		"systemctl --no-pager status sshd": false,
		"ip addr show":                     false,
		"hostnamectl status":               false,
		"hostname":                         false,
		"hostname -I":                      false,
		"cat /var/log/app.log":             false,
		"/usr/bin/uptime":                  false,
		"echo \"$(touch /tmp/pwned)\"":     true,
		"echo \"`touch /tmp/pwned`\"":      true,
		"PATH=/tmp ls":                     true,
		"/tmp/ls":                          true,
		"cat /etc/shadow":                  true,
		"cat ~/.ssh/id_rsa":                true,
		"cat /srv/app/.env":                true,
		"cat /var/log/*.log":               true,
		"find /tmp -delete":                true,
		"ps e":                             true,
		"ps auxeww":                        true,
		"cat \"$SECRET_FILE\"":             true,
		"awk '{system(\"touch /tmp/pwned\")}' file":   true,
		"cat ${SECRET_FILE}":                          true,
		"df -h || rm -rf /tmp/cache":                  true,
		"df -h; rm -rf /tmp/cache":                    true,
		"find / -type f 2>/tmp/find-errors.log":       true,
		"find / -type f 2>>/dev/null":                 true,
		"bash -c 'df -h'":                             true,
		"lastlog --clear":                             true,
		"docker inspect app":                          true,
		"git checkout main":                           true,
		"systemctl restart sshd":                      true,
		"ip link set eth0 down":                       true,
		"hostnamectl set-hostname changed":            true,
		"hostname -b":                                 true,
		"route add default gw 192.0.2.1":              true,
		"getent shadow":                               true,
		"rm -rf /tmp/cache":                           true,
		"curl https://example.invalid -o /tmp/result": true,
		"ls >/tmp/files":                              true,
		"unknown-command --status":                    true,
	}
	for command, expected := range tests {
		if actual := commandRequiresApproval(command); actual != expected {
			t.Errorf("commandRequiresApproval(%q) = %t, want %t", command, actual, expected)
		}
	}
}
