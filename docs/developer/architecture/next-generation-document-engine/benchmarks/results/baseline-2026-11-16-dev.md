# Engine benchmark 2026-11-16T14:16:32Z

- Host: dev MacBook Air (Apple M1, 8 GB RAM; Docker VM 8 CPU / 3.8 GiB, full stack + observability running)
- Git: `5a2bc2d-dirty` (the M1.7 harness and its fixes, uncommitted at run time)
- Corpus lock: `ed6875482ffe`
- Runs per job: 1
- Development numbers unless the host is the production-class one (doc 16 §5.3).

Pending corpus series (no files yet): real-complex-layout, real-forms, real-labelled-tables, real-presentations, real-scans

Peak RAM is the worker container's working set sampled once per second: a lower bound. Jobs shorter than the first stats frame (about 1 s) get no sample; "n/a" means no job of the row was sampled.

| Tool | Category | Jobs | Failed (unexpected) | Wall p50/p95 ms | Server p50/p95 ms | Peak RAM p50/max MiB | Checks passed |
|---|---|---|---|---|---|---|---|
| compress-pdf | hindi | 10 | 0 (0) | 299 / 681 | 139 / 292 | 46 / 46 | 30/40 |
| compress-pdf | image-heavy | 20 | 0 (0) | 880 / 2108 | 420 / 1477 | 38 / 93 | 60/60 |
| compress-pdf | malformed | 20 | 2 (0) | 294 / 713 | 81 / 288 | 86 / 86 | 22/28 |
| compress-pdf | multipage | 5 | 0 (0) | 1349 / 1646 | 1134 / 1325 | 46 / 84 | 15/15 |
| compress-pdf | pdf | 15 | 0 (0) | 731 / 2113 | 290 / 1353 | 32 / 79 | 45/45 |
| compress-pdf | scans | 12 | 0 (0) | 646 / 914 | 346 / 618 | 66 / 92 | 36/36 |
| excel-to-ods | office | 6 | 0 (0) | 1152 / 2944 | 1002 / 2620 | 342 / 364 | 6/6 |
| excel-to-pdf | office | 8 | 0 (0) | 1159 / 4568 | 1023 / 3923 | 367 / 410 | 32/32 |
| excel-to-pdf | spreadsheets | 3 | 0 (0) | 11166 / 26917 | 10723 / 26233 | 541 / 651 | 12/12 |
| html-to-pdf | html | 30 | 0 (0) | 609 / 921 | 380 / 786 | 336 / 390 | 120/120 |
| image-to-pdf | images | 15 | 0 (0) | 294 / 366 | 65 / 122 | 419 / 424 | 15/15 |
| merge-pdf | pdf | 5 | 0 (0) | 561 / 3557 | 196 / 912 | 27 / 30 | 5/5 |
| ocr-pdf | scans | 20 | 0 (0) | 13886 / 23009 | 13450 / 22635 | 218 / 336 | 40/60 |
| odp-to-pdf | office | 2 | 0 (0) | 2106 / 2725 | 1652 / 2461 | 353 / 355 | 8/8 |
| ods-to-pdf | office | 2 | 0 (0) | 857 / 1037 | 561 / 724 | 338 / 527 | 8/8 |
| ods-to-pdf | spreadsheets | 2 | 0 (0) | 10448 / 11897 | 9927 / 11413 | 583 / 635 | 8/8 |
| odt-to-pdf | office | 8 | 0 (0) | 889 / 1814 | 754 / 1623 | 276 / 545 | 32/32 |
| odt-to-pdf | tables | 6 | 0 (0) | 595 / 853 | 499 / 552 | 305 / 327 | 24/24 |
| pdf-metadata | pdf | 5 | 0 (0) | 482 / 1704 | 198 / 1274 | 26 / 31 | 15/15 |
| pdf-to-excel | tables | 8 | 0 (0) | 550 / 1103 | 431 / 834 | 67 / 126 | 8/8 |
| pdf-to-image | hindi | 10 | 0 (0) | 588 / 620 | 378 / 453 | 111 / 122 | 10/10 |
| pdf-to-image | image-heavy | 14 | 0 (0) | 1649 / 3508 | 1512 / 3105 | 59 / 176 | 14/14 |
| pdf-to-image | malformed | 20 | 12 (0) | 808 / 828 | 561 / 732 | 109 / 112 | 8/8 |
| pdf-to-image | multipage | 3 | 0 (0) | 53453 / 60654 | 52910 / 59516 | 147 / 161 | 3/3 |
| pdf-to-image | pdf | 5 | 0 (0) | 3934 / 4472 | 3601 / 4027 | 109 / 210 | 5/5 |
| pdf-to-odt | layout | 10 | 0 (0) | 1930 / 2698 | 1803 / 2164 | 169 / 354 | 10/10 |
| pdf-to-pdfa | hindi | 10 | 0 (0) | 295 / 733 | 110 / 345 | 97 / 97 | 50/60 |
| pdf-to-pdfa | pdf | 10 | 0 (0) | 895 / 1310 | 537 / 887 | 119 / 197 | 50/50 |
| pdf-to-text | hindi | 10 | 0 (0) | 306 / 845 | 41 / 497 | 110 / 113 | 10/10 |
| pdf-to-text | layout | 10 | 0 (0) | 363 / 453 | 68 / 109 | 30 / 78 | 10/10 |
| pdf-to-text | multipage | 2 | 0 (0) | 310 / 668 | 252 / 390 | 123 / 123 | 2/2 |
| pdf-to-text | pdf | 15 | 0 (0) | 471 / 3747 | 123 / 1756 | 79 / 191 | 15/15 |
| pdf-to-text | tables | 8 | 0 (0) | 353 / 431 | 83 / 188 | 29 / 271 | 8/8 |
| pdf-to-word | layout | 10 | 0 (0) | 1329 / 1652 | 1057 / 1433 | 91 / 141 | 10/10 |
| pdf-to-word | pdf | 5 | 0 (0) | 4379 / 16999 | 4117 / 14773 | 158 / 256 | 5/5 |
| pdf-to-word | tables | 8 | 0 (0) | 2127 / 2809 | 1885 / 2587 | 93 / 349 | 8/8 |
| powerpoint-to-pdf | office | 2 | 0 (0) | 607 / 1764 | 394 / 1538 | 432 / 551 | 8/8 |
| protect-pdf | pdf | 5 | 0 (0) | 380 / 839 | 35 / 203 | 29 / 29 | 5/5 |
| repair-pdf | malformed | 20 | 2 (0) | 300 / 324 | 102 / 193 | 27 / 97 | 22/28 |
| rotate-pdf | pdf | 5 | 0 (0) | 1295 / 2021 | 504 / 1035 | 27 / 36 | 5/5 |
| scan-to-pdf | images | 8 | 0 (0) | 300 / 315 | 30 / 51 | 21 / 22 | 8/8 |
| split-pdf | multipage | 3 | 0 (0) | 611 / 733 | 341 / 526 | 39 / 51 | 3/3 |
| split-pdf | pdf | 5 | 0 (0) | 384 / 662 | 72 / 307 | 31 / 42 | 5/5 |
| unlock-pdf | encrypted | 6 | 0 (0) | 299 / 315 | 27 / 76 | 45 / 47 | 12/12 |
| word-to-odt | office | 6 | 0 (0) | 1107 / 1219 | 803 / 932 | 346 / 358 | 6/6 |
| word-to-pdf | hindi | 10 | 0 (0) | 587 / 3141 | 492 / 2828 | 218 / 289 | 50/50 |
| word-to-pdf | layout | 10 | 0 (0) | 843 / 2406 | 565 / 2081 | 364 / 381 | 38/40 |
| word-to-pdf | office | 8 | 0 (0) | 978 / 2226 | 691 / 1844 | 349 / 391 | 32/32 |
| word-to-pdf | tables | 6 | 0 (0) | 837 / 1840 | 523 / 1587 | 268 / 281 | 24/24 |

## Findings (54)

- `scan-300dpi-001` ocr-pdf: check ssim = 0.8557 
- `scan-300dpi-002` ocr-pdf: check ssim = 0.8598 
- `scan-300dpi-003` ocr-pdf: check ssim = 0.8558 
- `scan-300dpi-004` ocr-pdf: check ssim = 0.8577 
- `scan-300dpi-005` ocr-pdf: check ssim = 0.8593 
- `scan-300dpi-006` ocr-pdf: check ssim = 0.8565 
- `scan-300dpi-007` ocr-pdf: check ssim = 0.8601 
- `scan-300dpi-008` ocr-pdf: check ssim = 0.858 
- `scan-300dpi-009` ocr-pdf: check ssim = 0.858 
- `scan-300dpi-010` ocr-pdf: check ssim = 0.8622 
- `scan-300dpi-011` ocr-pdf: check ssim = 0.8567 
- `scan-300dpi-012` ocr-pdf: check ssim = 0.8574 
- `scan-200dpi-001` ocr-pdf: check ssim = 0.8626 
- `scan-200dpi-002` ocr-pdf: check ssim = 0.8668 
- `scan-200dpi-003` ocr-pdf: check ssim = 0.8642 
- `scan-200dpi-004` ocr-pdf: check ssim = 0.8666 
- `scan-200dpi-005` ocr-pdf: check ssim = 0.8616 
- `scan-200dpi-006` ocr-pdf: check ssim = 0.8669 
- `scan-200dpi-007` ocr-pdf: check ssim = 0.8627 
- `scan-200dpi-008` ocr-pdf: check ssim = 0.8665 
- `hindi-pdf-001` compress-pdf: check text_cer = 0.8595 
- `hindi-pdf-001` pdf-to-pdfa: check text_cer = 0.8595 
- `hindi-pdf-002` compress-pdf: check text_cer = 0.8595 
- `hindi-pdf-002` pdf-to-pdfa: check text_cer = 0.8595 
- `hindi-pdf-003` compress-pdf: check text_cer = 0.8595 
- `hindi-pdf-003` pdf-to-pdfa: check text_cer = 0.8595 
- `hindi-pdf-004` compress-pdf: check text_cer = 0.8595 
- `hindi-pdf-004` pdf-to-pdfa: check text_cer = 0.8595 
- `hindi-pdf-005` compress-pdf: check text_cer = 0.8595 
- `hindi-pdf-005` pdf-to-pdfa: check text_cer = 0.8595 
- `hindi-pdf-006` compress-pdf: check text_cer = 0.8595 
- `hindi-pdf-006` pdf-to-pdfa: check text_cer = 0.8595 
- `hindi-pdf-007` compress-pdf: check text_cer = 0.8595 
- `hindi-pdf-007` pdf-to-pdfa: check text_cer = 0.8595 
- `hindi-pdf-008` compress-pdf: check text_cer = 0.8595 
- `hindi-pdf-008` pdf-to-pdfa: check text_cer = 0.8595 
- `hindi-pdf-009` compress-pdf: check text_cer = 0.8595 
- `hindi-pdf-009` pdf-to-pdfa: check text_cer = 0.8595 
- `hindi-pdf-010` compress-pdf: check text_cer = 0.8595 
- `hindi-pdf-010` pdf-to-pdfa: check text_cer = 0.8595 
- `columns-docx-002` word-to-pdf: check ssim = 0.9446 
- `columns-docx-005` word-to-pdf: check ssim = 0.9493 
- `malformed-006` repair-pdf: check text_cer = 0.04491 
- `malformed-006` compress-pdf: check text_cer = 0.04491 
- `malformed-008` repair-pdf: check pages_and_sizes = 0 pages 1, want 2
- `malformed-008` compress-pdf: check pages_and_sizes = 0 pages 1, want 2
- `malformed-010` repair-pdf: check pages_and_sizes = 0 pages 1, want 2
- `malformed-010` compress-pdf: check pages_and_sizes = 0 pages 1, want 2
- `malformed-016` repair-pdf: check text_cer = 0.04744 
- `malformed-016` compress-pdf: check text_cer = 0.04744 
- `malformed-018` repair-pdf: check pages_and_sizes = 0 pages 1, want 2
- `malformed-018` compress-pdf: check pages_and_sizes = 0 pages 1, want 2
- `malformed-020` repair-pdf: check pages_and_sizes = 0 pages 1, want 2
- `malformed-020` compress-pdf: check pages_and_sizes = 0 pages 1, want 2
