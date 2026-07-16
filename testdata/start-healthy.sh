#!/bin/sh
set -eu

classes=$1
pid_file=$2
ready_file=$3

/usr/bin/java -Xms16m -Xmx32m -cp "$classes" OomTestDaemon healthy "$pid_file" "$ready_file" &
