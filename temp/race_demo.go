package main

import (
    "fmt"
    "net/http"
    "sync"
)

func main() {
    url := "http://localhost:8080/check?user_id=raceUser"
    var wg sync.WaitGroup

    for i := 0; i < 15; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            resp, err := http.Get(url)
            if err == nil {
                fmt.Print(resp.StatusCode, " ")
                resp.Body.Close()
            }
        }()
    }
    wg.Wait()
    fmt.Println("\nRace demo completed – count how many 200s you see.")
}