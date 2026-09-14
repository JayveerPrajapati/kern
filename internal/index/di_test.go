package index

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFrameworkDIRepositoryCallers(t *testing.T) {
	dir := t.TempDir()

	// 1. Config with @Bean
	configJava := `package com.example.config;

import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration
public class AppConfig {
    @Bean
    public ServiceClient serviceClient() {
        return new ServiceClient();
    }
}
`
	// 2. Client class
	clientJava := `package com.example.config;

public class ServiceClient {
    public void execute() {}
}
`
	// 3. Consumer with @Autowired
	consumerJava := `package com.example.service;

import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.stereotype.Service;
import com.example.config.ServiceClient;

@Service
public class OrderService {
    @Autowired
    private ServiceClient serviceClient;

    public void processOrder() {
        serviceClient.execute();
    }
}
`

	_ = os.MkdirAll(filepath.Join(dir, "config"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "service"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "config", "AppConfig.java"), []byte(configJava), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "config", "ServiceClient.java"), []byte(clientJava), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "service", "OrderService.java"), []byte(consumerJava), 0o644)

	ix, err := Build(dir)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Verify AppConfig has OrderService as caller
	callers := ix.CallersFor(Symbol{Name: "AppConfig", Kind: "class"})
	if !contains(callers, "OrderService") {
		t.Errorf("expected OrderService in AppConfig callers, got %v", callers)
	}

	// Verify ServiceClient has OrderService as caller
	clientCallers := ix.CallersFor(Symbol{Name: "ServiceClient", Kind: "class"})
	if !contains(clientCallers, "OrderService") {
		t.Errorf("expected OrderService in ServiceClient callers, got %v", clientCallers)
	}
}
