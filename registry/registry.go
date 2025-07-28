package registry

import (
    "encoding/json"
    "os"
)

type Registry struct {
    Version string `json:"version"`
    Tools   []Tool `json:"tools"`
}

type Tool struct {
    Name        string `json:"name"`
    Alias       string `json:"alias"`
    Repository  string `json:"repository"`
    Binary      string `json:"binary"`
    Version     string `json:"version"`
    Description string `json:"description"`
}

var loadedRegistry *Registry

func LoadRegistry(path string) (*Registry, error) {
    if loadedRegistry != nil {
        return loadedRegistry, nil
    }

    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }

    var reg Registry
    if err := json.Unmarshal(data, &reg); err != nil {
        return nil, err
    }
    
    loadedRegistry = &reg
    return loadedRegistry, nil
}