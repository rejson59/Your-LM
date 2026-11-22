## YourLM 0.2.0-rc4 — natywne okno, worker w tle i szybkie ponowne ładowanie

To duże, spójne wydanie łączy najważniejsze poprawki Windows, Androida, wydajności i pamięci.

### Natywna aplikacja Windows

- Główny interfejs otwiera się bezpośrednio w oknie `YourLM.exe` przez WebView2, a nie w zwykłej karcie przeglądarki.
- Loader WebView2 jest osadzony w tym samym pojedynczym EXE; osobna przeglądarka pozostaje tylko awaryjnym fallbackiem dla brakującego lub uszkodzonego środowiska WebView2.
- Szybka kontrola wersjonowanych komponentów usuwa koszt ponownego rozpakowywania i pełnego haszowania dużego runtime przy każdym starcie.

### Redmi działa przy wyłączonym ekranie

- Worker został przeniesiony z cyklu życia Activity do usługi pierwszoplanowej Androida.
- Stałe powiadomienie pokazuje aktywną pracę i daje jawny przycisk **Zatrzymaj**.
- Blokada CPU oraz Wi-Fi high-performance utrzymują obliczenia po wygaszeniu ekranu, przejściu do ekranu głównego i zamknięciu widoku YourLM.
- Aplikacja nie zatrzymuje już workera w `onDestroy()`; aktualizacja przez ADB nadal używa `-r` i zachowuje dane.

### Znacznie szybsze kolejne uruchomienia modelu

- Serwery RPC Windows i Android używają trwałego, prywatnego cache tensorów llama.cpp (`--cache`). Pierwszy transfer przydzielonych wag przez Wi-Fi nadal może potrwać, ale ponowne uruchomienie tego samego modelu nie musi przesyłać ich ponownie.
- Bufory tunelu RPC zostały zwiększone, a połączenia TCP dostrojone do transferu dużych tensorów.
- Worker Android wykorzystuje wszystkie praktyczne rdzenie poza jednym pozostawionym dla systemu i interfejsu.
- Fazy startu, czas i zachowany log silnika z poprzedniej wersji pozostają dostępne.

### Uczciwy obraz całych 19,2 GiB

Panel rozdziela teraz:

1. pełny fizyczny RAM Lenovo + Redmi,
2. pamięć zajętą przez systemy i aplikacje,
3. RAM dostępny natychmiast,
4. pełny rozmiar zmapowanego pliku GGUF,
5. rzeczywistą rezydentną alokację procesów YourLM,
6. limit zatwierdzeń i wkład pliku stronicowania Windows.

Liczba „dostępne teraz” nie jest już prezentowana jako maksymalny rozmiar modelu. Model Huihui Gemma 4 26B-A4B 12,4 GiB pozostaje odblokowany: CPU-only nadal używa `--fit off`, a klaster używa natywnego `--fit on --fit-target 0`, bez wymuszonego `-ngl`, tensor splitu lub statycznego progu YourLM.

### Aktualizacje i pakiety

- Windows pozostaje jednym samodzielnym `YourLM.exe`.
- Android pozostaje jednym stabilnie podpisanym APK ARM64 z prostą instalacją/aktualizacją USB.
- Modele, ustawienia, komponenty i prywatny cache pozostają w katalogach danych i nie są kasowane przez podmianę EXE lub `adb install -r`.
- Aktualizator jest stale widoczny w bocznym pasku i automatycznie wykrywa wydania GitHub.

### Pliki

- `YourLM.exe` — Windows x64, pojedynczy plik.
- `YourLM-Android-arm64.apk` — Android 9+ ARM64, wersja kodu 7.
- `SHA256SUMS` — sumy SHA-256 obu plików.
