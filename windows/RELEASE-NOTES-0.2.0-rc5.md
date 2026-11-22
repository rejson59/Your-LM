## YourLM 0.2.0-rc5 — tryb ciemny i normalna aplikacja Windows

RC5 odpowiada na dwa problemy widoczne po wydaniu RC4: interfejs ma teraz pełny tryb ciemny, a pobrany pojedynczy EXE rejestruje YourLM jako zwykłą aplikację bieżącego użytkownika Windows.

### YourLM pojawia się w Menu Start

- Pierwsze uruchomienie `YourLM.exe` kopiuje zweryfikowany program do `%LOCALAPPDATA%\Programs\YourLM\YourLM.exe`.
- Tworzony jest skrót **YourLM** w Menu Start, więc program można znaleźć po naciśnięciu klawisza Windows.
- YourLM pojawia się również w **Ustawienia → Aplikacje → Zainstalowane aplikacje** i można go stamtąd odinstalować.
- Instalacja jest tylko dla bieżącego użytkownika i nie wymaga uprawnień administratora.
- Modele, logi, cache RPC i ustawienia pozostają w dotychczasowym `%APPDATA%\YourLM`; aktualizacja z RC4 niczego tam nie usuwa.
- Odinstalowanie usuwa program oraz skrót, ale celowo pozostawia prywatne modele i dane użytkownika.
- Opcja `--portable` nadal pozwala świadomie uruchomić EXE bez rejestracji w Windows.

### Pełny tryb ciemny

- Motyw przy pierwszym uruchomieniu podąża za ustawieniem Windows.
- Przełącznik jest dostępny w panelu bocznym i w nagłówku mobilnym.
- Wybór jest zachowywany między uruchomieniami, także gdy lokalny port interfejsu się zmienia.
- Ciemne warianty obejmują urządzenia, model/API, czat, formularze, okna dialogowe, aktualizacje i diagnostykę silnika.

### Czytelniejsze długie ładowanie GGUF

- Panel ładowania wyjaśnia, czy pełny GGUF jest mapowany z dysku, czy wagi są po raz pierwszy przesyłane do workera przez Wi-Fi.
- Ostrzeżenia modeli o tokenach kontrolnych i `mmap` są oznaczane jako informacyjne, zamiast wyglądać jak zawieszenie.
- Przy modelach MoE czas ładowania nadal zależy od pełnego rozmiaru pliku i wszystkich wag, a nie tylko liczby parametrów aktywnych dla jednego tokenu.

### Bezpieczeństwo i zgodność

- Instalacja nie uruchamia pobranych skryptów ani zewnętrznych instalatorów; kopiuje wyłącznie uruchomiony pojedynczy EXE. Sumę pliku pobranego ręcznie można sprawdzić w `SHA256SUMS`.
- Automatyczna aktualizacja przed uruchomieniem nowego EXE wymaga zgodnej sumy SHA-256 z GitHub Release.
- APK zachowuje ten sam stabilny certyfikat aktualizacji i ma `versionCode 8`.
- Runtime pozostaje przypięty do llama.cpp `c0b97be0b297177a88df08e96c01420976bb18d0` (`b12562`).

### Pliki wydania

- `YourLM.exe` — Windows x64, pojedynczy plik z instalacją per-user przy pierwszym uruchomieniu.
- `YourLM-Android-arm64.apk` — Android 9+ ARM64, wersja kodu 8.
- `SHA256SUMS` — sumy SHA-256 obu plików.
