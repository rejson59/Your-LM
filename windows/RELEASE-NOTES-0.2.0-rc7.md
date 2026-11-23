## YourLM 0.2.0-rc7 — poprawka awarii workera RPC podczas transferu wag

RC7 odpowiada na jednoznaczny błąd z RC6:

`ggml-rpc.cpp:699: Remote RPC server crashed or returned malformed response`

Ten komunikat nie pochodził od mmap ani ostrzeżeń tokenizera. Połączenie z workerem zostało przerwane podczas wysyłania danych tensora. W przypiętym llama.cpp tryb trwałego cache RPC tworzył na workerze tymczasową kopię całego przesyłanego tensora, mimo że automatyczny podział pamięci zdążył już przydzielić prawie cały wolny RAM. Duży tensor mógł przez to podwoić chwilowe zapotrzebowanie i doprowadzić do zakończenia natywnego procesu przez Androida.

### Poprawka

- Dane tensora oraz zapis cache są teraz przesyłane wspólnym buforem o rozmiarze najwyżej 16 MiB zamiast dodatkowej kopii całego tensora.
- Trafienia cache również są odczytywane porcjami, bez wczytywania całego pliku do drugiego bufora RAM.
- Plik cache staje się widoczny dopiero po pełnym zapisie. Przerwane transfery nie mogą być później uznane za poprawne trafienie.
- Niepełne, uszkodzone lub mające zły hash wpisy są wykrywane, usuwane i automatycznie przesyłane ponownie.
- Błąd zapisu cache, na przykład z powodu braku miejsca, nie przerywa już samego ładowania tensora; uruchomienie może kontynuować bez zapisania tego wpisu.
- Android i worker Windows usuwają osierocone pliki tymczasowe po poprzednim przerwanym procesie.
- Host RC7 odrzuca starszy, niepoprawiony worker przed startem modelu i prosi o aktualizację drugiego urządzenia.
- Panel Windows rozpoznaje ten konkretny błąd i podaje wskazówki dotyczące Redmi, pamięci telefonu i Wi-Fi.

### Ważne po aktualizacji

Zaktualizuj **oba urządzenia**. Zatrzymaj obecną próbę i worker, uruchom nowe `YourLM.exe`, a następnie w zakładce **Telefon USB** ponownie wybierz **Zainstaluj lub zaktualizuj**, aby Redmi otrzymał APK RC7 (`versionCode 10`) z poprawionym natywnym workerem. Uruchom worker na telefonie ponownie, usuń stary wpis Redmi na laptopie i sparuj nowym kodem. Samo zaktualizowanie aplikacji Windows nie podmienia automatycznie już zainstalowanej aplikacji telefonu.

Ostrzeżenia o tokenach i `tensor overrides to CPU ... mmap enabled` pozostają informacyjne. RC7 nadal nie wymusza globalnie `--load-mode none`.

Poprawkę sprawdza test cache obejmujący pierwszy zapis, trafienie, niepełny plik oraz plik o poprawnym rozmiarze, ale błędnej zawartości. Fizyczna próba konkretnego 26B GGUF na Lenovo i Redmi nadal zależy od dostępnego RAM-u, miejsca w pamięci telefonu i stabilności Wi-Fi.
