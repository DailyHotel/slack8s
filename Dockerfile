FROM golang:1.8.6-onbuild

RUN cat /usr/share/zoneinfo/Asia/Seoul > /etc/localtime


