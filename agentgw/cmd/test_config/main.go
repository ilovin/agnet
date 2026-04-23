package main

import (
    "fmt"
    "github.com/phone-talk/agentgw/internal/config"
)

func main() {
    cfg, err := config.Load("/Users/fengming.xie/.agentgw/config.json")
    if err != nil {
        fmt.Println("error:", err)
        return
    }
    fmt.Printf("port=%d\n", cfg.Port)
    fmt.Printf("tunnel.hub_url=%q\n", cfg.Tunnel.HubURL)
    fmt.Printf("tunnel.app_url=%q\n", cfg.Tunnel.AppURL)
    fmt.Printf("tunnel.reality_sni=%q\n", cfg.Tunnel.RealitySNI)
}
