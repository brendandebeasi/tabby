#!/usr/bin/env bash

for txt in tests/screenshots/current/*.txt; do
	baseline="tests/screenshots/baseline/$(basename "$txt")"
	if [ -f "$baseline" ]; then
		diff -u "$baseline" "$txt" > "tests/screenshots/diffs/$(basename "$txt").diff" || true
	fi
done

echo "Diffs saved to tests/screenshots/diffs/"
