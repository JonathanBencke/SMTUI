package config

import "os"

// starterContent is a heavily commented services.toml written on first run
// when no configuration exists. It ships the four presets the team uses
// (java-maven, wildfly, hcm-integration, node-npm), a [defaults.env] skeleton
// with generic placeholders, and commented [[service]] examples. Users then
// add real services through the web configuration page (key `c`) or by
// uncommenting and editing the examples below.
const starterContent = `# =============================================================================
#  services.toml — configuracao do Service Manager TUI
# -----------------------------------------------------------------------------
#  Este arquivo foi gerado automaticamente na primeira execucao.
#
#  Voce NAO precisa editar este arquivo na mao: pressione a tecla "c" dentro
#  do app para abrir a pagina web de configuracao no navegador e cadastre seus
#  servicos, presets, defaults e tenant de forma guiada. Ela grava o bloco
#  [[service]] aqui embaixo pra voce.
#
#  Se preferir editar manualmente, os exemplos comentados abaixo mostram o
#  formato de cada tipo de servico. Descomente e ajuste conforme necessario.
# =============================================================================

# -----------------------------------------------------------------------------
#  PRESETS — definem COMO cada tipo de servico builda e roda.
#  "build" e "run" sao templates Go; {{.Campo}} e substituido pelos valores do
#  servico. "env" sao variaveis de ambiente injetadas no processo.
# -----------------------------------------------------------------------------

# Java + Maven + Spring Boot (build: mvn install; run: exec-maven-plugin:java).
[presets.java-maven]
build = "mvn -pl {{.Modules}} -am install -DskipTests"
run = "mvn -pl impl compile org.codehaus.mojo:exec-maven-plugin:3.2.0:java -Dexec.mainClass={{.MainClass}} -Dexec.classpathScope=runtime{{if .Profiles}} -Dexec.args=--spring.profiles.active={{.Profiles}}{{end}}"
# Se o workdir do servico estiver dentro de um projeto Senior SDL/PDL/EDL (ou
# seja, existe um arquivo *.sdl, como main.sdl ou career.sdl, em algum
# diretorio pai, tipicamente workdir/../..), este comando e usado quando voce
# pede a geracao de fontes SOB DEMANDA (tecla "r" na TUI ou tool MCP
# generate_sources) e roda na raiz do projeto. Nunca roda ao iniciar o
# servico. Opcional: se omitido, o padrao "mvn clean generate-sources" e usado.
sdl_generate_command = "mvn clean generate-sources"
[presets.java-maven.env]
JAVA_HOME = "{{.JavaHome}}"
MAVEN_OPTS = "--add-opens java.base/java.time=ALL-UNNAMED --add-opens java.base/java.lang=ALL-UNNAMED"
PATH = '{{.JavaHome}}\bin;{{.Path}}'

# Servidores WildFly (run-only, sem build). O campo "main_class" de cada
# servico guarda o CAMINHO do .bat a executar (standalone.bat / domain.bat).
[presets.wildfly]
run = 'cmd /c "{{.MainClass}}"'
[presets.wildfly.env]
JAVA_HOME = "{{.JavaHome}}"

# Modulo Maven standalone executado in-process via exec-maven-plugin:java.
# O atalho r / tool MCP generate_sources compila o integrador e o monitor sem
# inicia-lo. Antes, copia o integration.properties do diretorio informado
# ({{.IntegrationPropertiesDir}}) para o workdir, pois o Main carrega
# ./integration.properties do diretorio atual. Defina o diretorio em
# [service.vars] IntegrationPropertiesDir. Se o frontend exigir uma versao
# especifica de Node.js, aponte {{.NodeHome}} para ela em [service.vars]
# NodeHome. Os --add-opens vao em MAVEN_OPTS porque o exec:java roda na JVM
# do Maven.
[presets.hcm-integration]
run = "mvn org.codehaus.mojo:exec-maven-plugin:3.2.0:java -Dexec.mainClass={{.MainClass}} -Dexec.classpathScope=runtime"
sdl_generate_command = 'cmd /c "copy /Y {{.IntegrationPropertiesDir}}\integration.properties integration.properties && pushd ..\hcm-updater && mvn clean package && popd && pushd ..\hcm-updater-integration && mvn clean package && popd && mvn clean package -DskipTests && pushd ..\hcm-monitor && npm install --no-audit --no-fund && npm run build && popd"'
generate_in_workdir = true
[presets.hcm-integration.env]
JAVA_HOME = "{{.JavaHome}}"
PATH = '{{.NodeHome}};{{.JavaHome}}\bin;{{.Path}}'
MAVEN_OPTS = "--add-opens java.base/java.time=ALL-UNNAMED --add-opens java.base/java.lang=ALL-UNNAMED"

# Frontends Node.js/Angular em modo de desenvolvimento (run-only).
[presets.node-npm]
run = "npm run dev"
# O atalho r / tool MCP generate_sources instala dependencias sem iniciar o app.
sdl_generate_command = "npm install --no-audit --no-fund"
generate_in_workdir = true

# Spring Boot "nativo" (plugin oficial spring-boot-maven-plugin): build empacota
# o artefato e run sobe a aplicacao com spring-boot:run, aplicando os perfis via
# -Dspring-boot.run.profiles. Diferente do preset java-maven (que usa o
# exec-maven-plugin da Senior), este e o modo padrao de rodar um Spring Boot.
# Para imagem nativa GraalVM, troque por:
#   build = "mvn -Pnative native:compile -DskipTests"
#   run   = ".\\target\\{{.MainClass}}"
[presets.spring-boot]
build = "mvn clean package -DskipTests"
run = "mvn spring-boot:run{{if .Profiles}} -Dspring-boot.run.profiles={{.Profiles}}{{end}}"
[presets.spring-boot.env]
JAVA_HOME = "{{.JavaHome}}"
PATH = '{{.JavaHome}}\bin;{{.Path}}'

# -----------------------------------------------------------------------------
#  DEFAULTS — variaveis de ambiente aplicadas a TODOS os servicos.
#  Substitua os placeholders pelos valores do seu ambiente.
# -----------------------------------------------------------------------------
[defaults]
[defaults.env]
BROKER_HOST = "SEU_BROKER.exemplo.com.br"
BROKER_PORT = "5674"
TENANT = "seu-tenant"
VIRTUAL_HOST = "seu-tenant"
DIAGNOSTIC_PORT = "0"

# -----------------------------------------------------------------------------
#  SERVICOS — exemplos comentados. Prefira usar a pagina web de configuracao
#  (tecla "c") para cadastrar; ou descomente um bloco abaixo e ajuste os valores.
# -----------------------------------------------------------------------------

# Exemplo: backend Java/Maven
# [[service]]
# name = "Meu Backend"
# runner = "java-maven"
# workdir = "../meu-backend/java"
# java_home = 'C:\Program Files\Java\JDK17.0.16'
# modules = ["client", "server"]
# main_class = "br.com.exemplo.MeuServer"
# profiles = ["dev"]
# health_port = 0

# Exemplo: frontend Node/Angular
# [[service]]
# name = "Meu Frontend"
# runner = "node-npm"
# workdir = "../meu-frontend"
# health_port = 0

# Exemplo: servidor WildFly (main_class = caminho do .bat)
# [[service]]
# name = "WildFly"
# runner = "wildfly"
# workdir = 'C:\wildfly-30.0.1.Final\bin'
# main_class = 'C:\wildfly-30.0.1.Final\bin\standalone.bat'
# java_home = 'C:\Program Files\Java\JDK17.0.16'
# health_port = 0
# [service.env]
# JAVA_OPTS = "-Xms128M -Xmx512M"
`

// WriteStarter writes the commented starter services.toml to path. It refuses
// to overwrite an existing file.
func WriteStarter(path string) error {
	if Exists(path) {
		return nil
	}
	return os.WriteFile(path, []byte(starterContent), 0644)
}
