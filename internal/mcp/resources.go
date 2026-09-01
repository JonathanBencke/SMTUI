package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/JonathanBencke/ServiceManagerTUI/internal/config"
	"github.com/JonathanBencke/ServiceManagerTUI/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

// Resource URIs published by the server. Clients can subscribe to them and be
// notified when the underlying state changes, instead of polling tools.
const (
	uriServices   = "smtui://services"
	uriConfig     = "smtui://config"
	uriPresets    = "smtui://presets"
	servicePrefix = uriServices + "/"
	mimeJSON      = "application/json"
	mimePlainText = "text/plain"
	mimeTOML      = "application/toml"
)

func (m *MCPServer) registerResources() {
	m.srv.AddResource(
		mcp.NewResource(uriServices, "Services",
			mcp.WithResourceDescription("Live status of every managed service (same payload as list_services). Subscribe to be notified when a service starts, stops or crashes."),
			mcp.WithMIMEType(mimeJSON),
		),
		m.readServicesResource,
	)

	m.srv.AddResource(
		mcp.NewResource(uriConfig, "Configuration file",
			mcp.WithResourceDescription("The raw services.toml driving the tool. Read-only: the MCP server never writes configuration."),
			mcp.WithMIMEType(mimeTOML),
		),
		m.readConfigResource,
	)

	m.srv.AddResource(
		mcp.NewResource(uriPresets, "Presets",
			mcp.WithResourceDescription("Reusable build/run recipes (presets) declared in the configuration, with the template variables each one expects."),
			mcp.WithMIMEType(mimeJSON),
		),
		m.readPresetsResource,
	)

	m.srv.AddResourceTemplate(
		mcp.NewResourceTemplate(servicePrefix+"{name}", "Service",
			mcp.WithTemplateDescription("Runtime state of a single service."),
			mcp.WithTemplateMIMEType(mimeJSON),
		),
		m.readServiceResource,
	)

	m.srv.AddResourceTemplate(
		mcp.NewResourceTemplate(servicePrefix+"{name}/logs", "Service logs",
			mcp.WithTemplateDescription("Buffered log output of a single service. Subscribe to be notified when new lines arrive."),
			mcp.WithTemplateMIMEType(mimePlainText),
		),
		m.readServiceLogsResource,
	)

	m.srv.AddResourceTemplate(
		mcp.NewResourceTemplate(servicePrefix+"{name}/config", "Service configuration",
			mcp.WithTemplateDescription("Effective configuration of a single service, with expanded build/run commands and redacted environment values."),
			mcp.WithTemplateMIMEType(mimeJSON),
		),
		m.readServiceConfigResource,
	)
}

func (m *MCPServer) readServicesResource(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	return jsonResource(req.Params.URI, newServiceListDTO(m.manager.Services()))
}

func (m *MCPServer) readConfigResource(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	path := m.configPath()
	if path == "" {
		return nil, fmt.Errorf("no configuration file is associated with this server")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return []mcp.ResourceContents{mcp.TextResourceContents{
		URI:      req.Params.URI,
		MIMEType: mimeTOML,
		Text:     string(data),
	}}, nil
}

func (m *MCPServer) readPresetsResource(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	path := m.configPath()
	if path == "" {
		return nil, fmt.Errorf("no configuration file is associated with this server")
	}
	cfg, err := config.LoadRaw(path)
	if err != nil {
		return nil, err
	}
	return jsonResource(req.Params.URI, newPresetListDTO(cfg))
}

func (m *MCPServer) readServiceResource(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	s, err := m.serviceFromURI(req.Params.URI, "")
	if err != nil {
		return nil, err
	}
	return jsonResource(req.Params.URI, newServiceDTO(s))
}

func (m *MCPServer) readServiceLogsResource(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	s, err := m.serviceFromURI(req.Params.URI, "logs")
	if err != nil {
		return nil, err
	}
	return []mcp.ResourceContents{mcp.TextResourceContents{
		URI:      req.Params.URI,
		MIMEType: mimePlainText,
		Text:     strings.Join(s.Logs(), "\n"),
	}}, nil
}

func (m *MCPServer) readServiceConfigResource(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	s, err := m.serviceFromURI(req.Params.URI, "config")
	if err != nil {
		return nil, err
	}
	return jsonResource(req.Params.URI, newServiceConfigDTO(s, false))
}

// serviceFromURI extracts the service name from smtui://services/{name}[/suffix]
// and resolves it, failing with a message that lists the valid names.
func (m *MCPServer) serviceFromURI(uri, suffix string) (*service.Service, error) {
	rest := strings.TrimPrefix(uri, servicePrefix)
	if rest == uri {
		return nil, fmt.Errorf("unsupported resource URI %q", uri)
	}
	if suffix != "" {
		trimmed := strings.TrimSuffix(rest, "/"+suffix)
		if trimmed == rest {
			return nil, fmt.Errorf("unsupported resource URI %q", uri)
		}
		rest = trimmed
	}

	name, err := url.PathUnescape(rest)
	if err != nil {
		name = rest
	}
	if name == "" {
		return nil, fmt.Errorf("missing service name in resource URI %q", uri)
	}

	s := m.manager.ServiceByName(name)
	if s == nil {
		return nil, fmt.Errorf("service %q not found. %s", name, m.knownServicesHint())
	}
	return s, nil
}

// serviceURI builds the base resource URI of a service, escaping names that
// contain characters not allowed in a path segment.
func serviceURI(name string) string {
	return servicePrefix + url.PathEscape(name)
}

func jsonResource(uri string, payload any) ([]mcp.ResourceContents, error) {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, err
	}
	return []mcp.ResourceContents{mcp.TextResourceContents{
		URI:      uri,
		MIMEType: mimeJSON,
		Text:     string(data),
	}}, nil
}
