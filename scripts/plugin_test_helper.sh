#!/bin/bash

cd ..
plugins=$(ls -1 .. | grep gbp-)
curpath=$(pwd)
upperpath=$(
	cd ..
	pwd
	cd $curpath
)

echo $plugins
echo $curpath
echo $upperpath

for p in ${plugins}; do
	cd ${upperpath}/${p}
	make build
	mv lib/*.so ${curpath}/plugins
done

cd ${curpath}/scripts
