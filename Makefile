# Alvos do plano (Fase 1). Cada módulo do go.work é tratado em loop, porque
# "./..." na raiz do workspace não atravessa módulos.
GO      ?= go
MODULES := platform ms-ingest ms-worker ms-notification ms-api ms-queues archtest
# archtest tem alvo próprio (arch); tests só roda com Docker (integration)
TEST_MODULES := $(filter-out archtest,$(MODULES))
SERVICES := ms-ingest ms-worker ms-notification ms-api

.PHONY: build vet test lint vuln arch integration check images accept tidy load

build:
	@for m in $(MODULES) tests; do (cd $$m && $(GO) build -o /dev/null ./...) || exit 1; done

vet:
	@for m in $(MODULES); do (cd $$m && $(GO) vet ./...) || exit 1; done
	@(cd tests && $(GO) vet -tags "integration load" ./...)
	@(cd ms-worker && $(GO) vet -tags faultinject ./...)

test:
	@for m in $(TEST_MODULES); do (cd $$m && $(GO) test -race -count=1 ./...) || exit 1; done

arch:
	@cd archtest && $(GO) test -count=1 ./...

lint:
	@for m in $(MODULES) tests; do (cd $$m && golangci-lint run --build-tags integration,load,faultinject ./...) || exit 1; done

vuln:
	@for m in $(MODULES) tests; do (cd $$m && govulncheck ./...) || exit 1; done

# precisa de Docker; sobe um RabbitMQ 4 com testcontainers-go
integration:
	@cd tests && $(GO) test -tags integration -race -count=1 ./...

images:
	@for s in $(SERVICES); do docker build -q --build-arg PKG=./$$s/cmd/$$s --target final -t $$s-app . || exit 1; done
	@docker build -q --build-arg PKG=./ms-queues/cmd/topology-init --target final-job -t ms-queues-topology-init .
	@docker build -q --target https-probe -t ms-go-https-probe .

tidy:
	@for m in $(MODULES) tests; do (cd $$m && $(GO) mod tidy) || exit 1; done
	@$(GO) work sync

# G2 (Fase 7): carga com números reais; demora, por isso fora do accept
load:
	@scripts/carga-fase-7.sh

# Definição de pronto da fase: tudo com saída 0 (conferir "echo $$?" sem pipe)
check: build vet test arch lint vuln

accept:
	@scripts/aceite-fase-1.sh
	@scripts/aceite-fase-2.sh
	@scripts/aceite-fase-3.sh
	@scripts/aceite-fase-4.sh
	@scripts/aceite-fase-5.sh
	@scripts/aceite-fase-6.sh
	@scripts/aceite-fase-7.sh
	@scripts/aceite-fase-8.sh
