#!/bin/bash

## Generate cert and put in doorphoneserver folder

openssl genrsa -aes256 -out key.pem
openssl req -new -x509 -key key.pem -out cert.pem -days 1095
openssl rsa -in key.pem -out nopasskey.pem
cat nopasskey.pem cert.pem > ../mumble.pem

