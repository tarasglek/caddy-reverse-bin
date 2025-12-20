#!/bin/bash

printf "Content-type: text/plain\n\n"

printf "Hello, World!\n"
printf "PATH_INFO    [%s]\n" "$PATH_INFO"
printf "QUERY_STRING [%s]\n" "$QUERY_STRING"

exit 0
