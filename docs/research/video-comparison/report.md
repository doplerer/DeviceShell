# iPhone video over USB: QuickTime vs. DisplayService H.264

2026-11-01 23:19. Measured by `TestCompareCableVideo` with automatic swipes between home screen pages.

|  | QuickTime | DisplayService H.264 |
|---|---|---|
| First frame | 1309 ms | 348 ms |
| Resolution | 1320x2868 | 1328x2896 |
| fps at rest | 49.6 | 46.4 |
| fps while swiping | 56.6 | 49.6 |
| fps, 2 min of swiping | 56.3 | 47.9 |
| Mbit/s at rest | 10.23 | 0.10 |
| Mbit/s while swiping | 32.01 | 4.87 |
| Tap-to-frame latency (median) | 165 ms | 201 ms |
| Tap-to-frame latency (p90) | 169 ms | 233 ms |
| Sharpness at rest | 927 | 809 |
| Sharpness while swiping | 785 | 732 |
| PC CPU while swiping (cores) | 0.43 | 0.19 |
| Decode time per frame | 6.05 ms | 3.03 ms |
| PC CPU over 2 min (cores) | 0.31 | 0.16 |
| iPhone battery at start | — | — |
| iPhone battery after ~3 min | — | — |

Sharpness is the variance of the Laplacian of the luminance in the central third of the frame (higher means more detail).
