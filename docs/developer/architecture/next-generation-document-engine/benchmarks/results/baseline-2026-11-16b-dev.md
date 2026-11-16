# Engine benchmark 2026-11-16T15:45:19Z

- Host: dev MacBook Air (Apple M1, 8 GB RAM; Docker VM 8 CPU / 3.8 GiB, full stack + observability running)
- Git: `1105c6d-dirty` (with the fixes for the first run's findings: output guards, OCR overlay, base image without libpq; uncommitted at run time)
- Corpus: pdf-small/medium/large now carry a real image (not unreferenced padding), so this run is not comparable with `baseline-2026-11-16-dev` for those rows.
- Noise floor on this host (two same-code runs, `noise-2026-11-16-r*.json`): 57 % wall p50 at worst (LibreOffice tools 24–57 %), under 10 % for the rest. Single-run timing deltas here are not evidence.
- Corpus lock: `395100d36f88`
- Runs per job: 1
- Development numbers unless the host is the production-class one (doc 16 §5.3).

Pending corpus series (no files yet): real-complex-layout, real-forms, real-labelled-tables, real-presentations, real-scans

Peak RAM is the worker container's working set sampled once per second: a lower bound. Jobs shorter than the first stats frame (about 1 s) get no sample; "n/a" means no job of the row was sampled.

| Tool | Category | Jobs | Failed (unexpected) | Wall p50/p95 ms | Server p50/p95 ms | Peak RAM p50/max MiB | Checks passed |
|---|---|---|---|---|---|---|---|
| compress-pdf | hindi | 10 | 0 (0) | 558 / 1529 | 284 / 1109 | 82 / 99 | 40/40 |
| compress-pdf | image-heavy | 20 | 0 (0) | 595 / 829 | 306 / 602 | 19 / 31 | 60/60 |
| compress-pdf | malformed | 20 | 2 (0) | 298 / 562 | 149 / 489 | 90 / 124 | 34/36 |
| compress-pdf | multipage | 5 | 0 (0) | 1193 / 1893 | 919 / 1591 | 51 / 62 | 15/15 |
| compress-pdf | pdf | 15 | 0 (0) | 1416 / 4577 | 1061 / 3654 | 50 / 94 | 45/45 |
| compress-pdf | scans | 12 | 0 (0) | 644 / 912 | 492 / 669 | 38 / 65 | 36/36 |
| excel-to-ods | office | 6 | 0 (0) | 856 / 2011 | 637 / 1777 | 270 / 349 | 6/6 |
| excel-to-pdf | office | 8 | 0 (0) | 1120 / 1690 | 950 / 1373 | 310 / 456 | 32/32 |
| excel-to-pdf | spreadsheets | 3 | 0 (0) | 16999 / 28275 | 16048 / 26400 | 550 / 655 | 12/12 |
| html-to-pdf | html | 30 | 0 (0) | 585 / 888 | 359 / 759 | 363 / 437 | 120/120 |
| image-to-pdf | images | 15 | 0 (0) | 311 / 443 | 84 / 190 | 367 / 394 | 15/15 |
| merge-pdf | pdf | 5 | 0 (0) | 495 / 762 | 130 / 159 | 20 / 28 | 5/5 |
| ocr-pdf | scans | 20 | 0 (0) | 19141 / 29833 | 18733 / 29558 | 185 / 257 | 60/60 |
| odp-to-pdf | office | 2 | 0 (0) | 591 / 887 | 499 / 663 | 319 / 319 | 8/8 |
| ods-to-pdf | office | 2 | 0 (0) | 586 / 654 | 479 / 510 | 310 / 497 | 8/8 |
| ods-to-pdf | spreadsheets | 2 | 0 (0) | 12991 / 13012 | 12508 / 12712 | 518 / 576 | 8/8 |
| odt-to-pdf | office | 8 | 0 (0) | 585 / 892 | 480 / 738 | 258 / 486 | 32/32 |
| odt-to-pdf | tables | 6 | 0 (0) | 592 / 894 | 392 / 625 | 358 / 367 | 24/24 |
| pdf-metadata | pdf | 5 | 0 (0) | 376 / 379 | 49 / 130 | 27 / 30 | 15/15 |
| pdf-to-excel | tables | 8 | 0 (0) | 835 / 1111 | 584 / 888 | 89 / 102 | 8/8 |
| pdf-to-image | hindi | 10 | 0 (0) | 856 / 1857 | 546 / 1495 | 104 / 126 | 10/10 |
| pdf-to-image | image-heavy | 14 | 0 (0) | 2138 / 4116 | 1969 / 3840 | 91 / 106 | 14/14 |
| pdf-to-image | malformed | 20 | 12 (0) | 807 / 1074 | 560 / 953 | 83 / 91 | 8/8 |
| pdf-to-image | multipage | 3 | 0 (0) | 65425 / 69384 | 65020 / 69002 | 108 / 111 | 3/3 |
| pdf-to-image | pdf | 5 | 0 (0) | 5344 / 11576 | 5044 / 10963 | 153 / 288 | 5/5 |
| pdf-to-odt | layout | 10 | 0 (0) | 2153 / 4951 | 2038 / 4841 | 219 / 408 | 10/10 |
| pdf-to-pdfa | hindi | 10 | 0 (0) | 296 / 568 | 248 / 395 | 102 / 113 | 50/60 |
| pdf-to-pdfa | pdf | 10 | 0 (0) | 1141 / 1780 | 800 / 1432 | 50 / 295 | 50/50 |
| pdf-to-text | hindi | 10 | 0 (0) | 307 / 614 | 41 / 290 | 102 / 104 | 10/10 |
| pdf-to-text | layout | 10 | 0 (0) | 346 / 451 | 79 / 143 | 129 / 221 | 10/10 |
| pdf-to-text | multipage | 2 | 0 (0) | 362 / 622 | 236 / 289 | 73 / 73 | 2/2 |
| pdf-to-text | pdf | 15 | 0 (0) | 518 / 2301 | 184 / 1232 | 32 / 273 | 15/15 |
| pdf-to-text | tables | 8 | 0 (0) | 364 / 411 | 95 / 124 | 55 / 115 | 8/8 |
| pdf-to-word | layout | 10 | 0 (0) | 1320 / 3248 | 1159 / 3158 | 181 / 258 | 10/10 |
| pdf-to-word | pdf | 5 | 0 (0) | 4169 / 6702 | 3861 / 6371 | 266 / 323 | 5/5 |
| pdf-to-word | tables | 8 | 0 (0) | 2377 / 4876 | 2241 / 4585 | 118 / 302 | 8/8 |
| powerpoint-to-pdf | office | 2 | 0 (0) | 1173 / 1210 | 803 / 839 | 503 / 516 | 8/8 |
| protect-pdf | pdf | 5 | 0 (0) | 387 / 455 | 106 / 151 | 19 / 19 | 5/5 |
| repair-pdf | malformed | 20 | 2 (0) | 295 / 572 | 153 / 414 | 100 / 117 | 34/36 |
| rotate-pdf | pdf | 5 | 0 (0) | 496 / 580 | 175 / 337 | 18 / 46 | 5/5 |
| scan-to-pdf | images | 8 | 0 (0) | 359 / 706 | 64 / 241 | 21 / 21 | 8/8 |
| split-pdf | multipage | 3 | 0 (0) | 341 / 346 | 149 / 173 | 27 / 27 | 3/3 |
| split-pdf | pdf | 5 | 0 (0) | 372 / 429 | 100 / 140 | 57 / 57 | 5/5 |
| unlock-pdf | encrypted | 6 | 0 (0) | 310 / 347 | 24 / 94 | 43 / 43 | 12/12 |
| word-to-odt | office | 6 | 0 (0) | 1108 / 1534 | 835 / 1131 | 267 / 319 | 6/6 |
| word-to-pdf | hindi | 10 | 0 (0) | 609 / 8485 | 498 / 8337 | 292 / 329 | 50/50 |
| word-to-pdf | layout | 10 | 0 (0) | 644 / 2270 | 509 / 1958 | 141 / 183 | 38/40 |
| word-to-pdf | office | 8 | 0 (0) | 891 / 1395 | 580 / 1064 | 297 / 428 | 32/32 |
| word-to-pdf | tables | 6 | 0 (0) | 1351 / 2983 | 873 / 2334 | 346 / 359 | 24/24 |

## Findings (16)

- `hindi-pdf-001` pdf-to-pdfa: check text_cer = 0.8595 
- `hindi-pdf-002` pdf-to-pdfa: check text_cer = 0.8595 
- `hindi-pdf-003` pdf-to-pdfa: check text_cer = 0.8595 
- `hindi-pdf-004` pdf-to-pdfa: check text_cer = 0.8595 
- `hindi-pdf-005` pdf-to-pdfa: check text_cer = 0.8595 
- `hindi-pdf-006` pdf-to-pdfa: check text_cer = 0.8595 
- `hindi-pdf-007` pdf-to-pdfa: check text_cer = 0.8595 
- `hindi-pdf-008` pdf-to-pdfa: check text_cer = 0.8595 
- `hindi-pdf-009` pdf-to-pdfa: check text_cer = 0.8595 
- `hindi-pdf-010` pdf-to-pdfa: check text_cer = 0.8595 
- `columns-docx-002` word-to-pdf: check ssim = 0.9446 
- `columns-docx-005` word-to-pdf: check ssim = 0.9493 
- `malformed-006` repair-pdf: check text_cer = 0.04491 
- `malformed-006` compress-pdf: check text_cer = 0.04491 
- `malformed-016` repair-pdf: check text_cer = 0.04744 
- `malformed-016` compress-pdf: check text_cer = 0.04744 
