# Scenario: Redis Sentinel HA Failover Demo
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "Scenario: Redis Sentinel HA Failover Demo" -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "In Sentinel mode, client automatically redirects writes to promoted replica." -ForegroundColor Yellow
Write-Host "If running the Sentinel HA stack, kill the master container:" -ForegroundColor Yellow
Write-Host "  docker stop redis-master" -ForegroundColor DarkYellow
Write-Host ""
Write-Host "For single-node setup, restarting Redis performs a quick recovery cycle:" -ForegroundColor Yellow
docker restart rate-redis
