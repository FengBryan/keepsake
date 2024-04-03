#!/bin/bash -eu

INSTALL_MESSAGE="Please follow the instructions on https://golang.org/doc/install to install the latest version of Go."

GO_VERSION=$(go version 2>&1 || true)
semver_compare() {
  local version_a version_b pr_a pr_b
  # strip word "v" and extract first subset version (x.y.z from x.y.z-foo.n)
  version_a=$(echo "${1//v/}" | awk -F'-' '{print $1}')
  version_b=$(echo "${2//v/}" | awk -F'-' '{print $1}')

  if [ "$version_a" \= "$version_b" ]
  then
    # check for pre-release
    # extract pre-release (-foo.n from x.y.z-foo.n)
    pr_a=$(echo "$1" | awk -F'-' '{print $2}')
    pr_b=$(echo "$2" | awk -F'-' '{print $2}')

    ####
    # Return 0 when A is equal to B
    [ "$pr_a" \= "$pr_b" ] && echo 0 && return 0

    ####
    # Return 1

    # Case when A is not pre-release
    if [ -z "$pr_a" ]
    then
      echo 1 && return 0
    fi

    ####
    # Case when pre-release A exists and is greater than B's pre-release

    # extract numbers -rc.x --> x
    number_a=$(echo ${pr_a//[!0-9]/})
    number_b=$(echo ${pr_b//[!0-9]/})
    [ -z "${number_a}" ] && number_a=0
    [ -z "${number_b}" ] && number_b=0

    [ "$pr_a" \> "$pr_b" ] && [ -n "$pr_b" ] && [ "$number_a" -gt "$number_b" ] && echo 1 && return 0

    ####
    # Retrun -1 when A is lower than B
    echo -1 && return 0
  fi
  arr_version_a=(${version_a//./ })
  arr_version_b=(${version_b//./ })
  cursor=0
  # Iterate arrays from left to right and find the first difference
  while [ "$([ "${arr_version_a[$cursor]}" -eq "${arr_version_b[$cursor]}" ] && [ $cursor -lt ${#arr_version_a[@]} ] && echo true)" == true ]
  do
    cursor=$((cursor+1))
  done
  [ "${arr_version_a[$cursor]}" -gt "${arr_version_b[$cursor]}" ] && echo 1 || echo -1
}

if $(echo "$GO_VERSION" | grep -q -E 'go: (command )?not found'); then
    echo "ERROR: Go is not installed."
    echo
    echo "$INSTALL_MESSAGE"
    exit 1
fi

if $(echo "$GO_VERSION" | grep -q -E -v "go version"); then
    echo "ERROR: failed to determine go version, 'go version' returned:"
    echo "  $GO_VERSION"
    echo
    echo "$INSTALL_MESSAGE"
    exit 1
fi

GO_VERSION_NUMBER=$(echo "$GO_VERSION" | sed -E 's/^go version go([^ ]+) .+$/\1/')
flag=$(semver_compare $GO_VERSION_NUMBER "1.14")
if [ "$flag" != "1" ]; then
    echo "ERROR: Unsupported Go version: $GO_VERSION_NUMBER"
    echo "Keepsake requires Go >= 1.14"
    echo
    echo "$INSTALL_MESSAGE"
    exit 1
fi
