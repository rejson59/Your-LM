# YourLM 0.2.0-rc14 — odpowiedź czatu na żywo i szybkie zatrzymanie

RC14 naprawia sytuację, w której duży model wyglądał na zawieszony, mimo że bardzo wolno generował pełne 512 tokenów. Transport USB/ADB z RC13 działał poprawnie, ale przy modelu 24–26B ograniczeniem pozostaje obliczanie na CPU, a nie przesyłanie danych.

## Czat pokazuje odpowiedź od pierwszego tokenu

- Wbudowany czat używa strumienia OpenAI/SSE i wyświetla każdy odebrany fragment natychmiast zamiast czekać na zakończenie całej odpowiedzi.
- Podczas generowania przycisk **Wyślij** zmienia się w **Zatrzymaj**. Przerwanie anuluje żądanie do `llama-server`, zwalnia slot i zachowuje już pokazany fragment.
- Domyślna szybka próba ma limit 64 tokenów. Przed wysłaniem można wybrać 128, 256 albo 512 tokenów.
- Panel pokazuje bieżący napływ fragmentów oraz końcową prędkość raportowaną przez silnik.
- Dla bardzo dużego GGUF panel wyjaśnia, że USB skraca transfer wag, ale nie zwiększa wydajności CPU podczas generowania.

## Ostrzeżenie o wadliwym tokenie końca

Log zgłoszony dla `gemma-4-26B-A4B...Q4_K_M.gguf` zawierał `removing it from EOG list`, po czym model wygenerował dokładnie pełny limit 512 tokenów. RC14 nie traktuje już tego ostrzeżenia jako wyłącznie informacyjnego: panel ostrzega, że konkretny GGUF może nie zatrzymać odpowiedzi samodzielnie i zaleca krótki limit albo inny plik modelu.

## Co pokazał zgłoszony pomiar

- USB/ADB: około 29,2 MiB/s wysyłania, 34,9 MiB/s odbioru i 3,8 ms pingu.
- Wi‑Fi: około 3,05 MiB/s wysyłania, 3,67 MiB/s odbioru i 32 ms pingu.
- Wybór USB był prawidłowy i wyraźnie skrócił transport.
- Sam model generował około 0,64 tokena/s. Przy starym limicie 512 oznaczało to około 13–14 minut samego generowania, niezależnie od szybkiego kabla.

Do interaktywnej rozmowy na CPU nadal zalecany jest model 3–4B Q4_K_M. Model 24–26B może działać poprawnie, ale zwykle nie zapewni czasu odpowiedzi podobnego do chmurowego czatu.

## Kompatybilność i bezpieczeństwo

Zgodność mostu pozostaje `b12562-ylm-rpc5`. Przypięcie certyfikatu TLS, sekret sesji, lokalny surowy RPC i selektywne przekierowanie ADB pozostają bez zmian. Android ma `versionCode 17`; APK zachowuje stabilny certyfikat aktualizacji projektu.
