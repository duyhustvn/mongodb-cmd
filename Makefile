IMG=mongodb-cmd-go
IMG_TAG=1.0.0

PROXY ?=
NO_PROXY ?=

docker-build:
	docker build \
		--build-arg http_proxy=$(PROXY) \
		--build-arg https_proxy=$(PROXY) \
		--build-arg no_proxy=$(PROXY) \
		--build-arg HTTP_PROXY=$(PROXY) \
		--build-arg HTTPS_PROXY=$(PROXY) \
		--build-arg NO_PROXY=$(PROXY) \
		-t ${IMG}:${IMG_TAG} .

docker-save:
	docker save -o ${IMG}.tar ${IMG}:${IMG_TAG}