[project]
name = ssdb-keeper
version = 0.9.0
vendor = sysinner.com
homepage = http://www.sysinner.com
groups = dev/db
description = configuration management tool for ssdb

%build

rm -rf   {{.buildroot}}/*
mkdir -p {{.buildroot}}/bin
mkdir -p {{.buildroot}}/misc
mkdir -p {{.buildroot}}/var/log

cd {{.inpack__pack_dir}}

go build -ldflags "-w -s" -o {{.buildroot}}/bin/ssdb-keeper main.go
install misc/ssdb.conf.default      {{.buildroot}}/misc/ssdb.conf.default

%files

