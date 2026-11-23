# YourLM 0.2.0-rc13 — automatyczny wybór USB/ADB lub Wi‑Fi

RC13 zachowuje bezpieczne odzyskiwanie pamięci z RC12, odporny worker Android i wznawiane pobieranie GGUF. Nowa wersja mierzy rzeczywisty, szyfrowany transport do telefonu i sama wybiera szybszą dostępną drogę.

## Pomiar połączenia przed startem modelu

- Po sparowaniu Android zgłasza typ platformy, a Windows sprawdza autoryzowane urządzenia widoczne przez fizyczny kabel ADB. Połączenia ADB over Wi‑Fi i emulatory nie są traktowane jako kabel.
- Host tworzy dynamiczny, lokalny `adb forward` do dodatkowego listenera TLS na `127.0.0.1` telefonu. Listener używa tego samego certyfikatu, przypiętego odcisku i sekretu sesji co most Wi‑Fi.
- Surowy `ggml-rpc-server` nadal słucha wyłącznie na prywatnym loopbacku Androida. ADB nie dostaje bezpośredniego portu RPC i nie omija parowania.
- Worker udostępnia po uwierzytelnieniu dwa ograniczone testy: 4 MiB host → telefon i 4 MiB telefon → host. TLS sprawia, że zerowy bufor nie staje się sztucznie kompresowalnym wynikiem ADB.
- YourLM mierzy też medianę pięciu uwierzytelnionych pingów. Wynik przepustowości ma wagę 75% dla wysyłania host → worker i 25% dla odbioru; gdy wyniki różnią się o nie więcej niż 5%, rozstrzyga niższe opóźnienie.
- Test wykonywany jest po sparowaniu, po ręcznym odświeżeniu USB i ponownie bezpośrednio przed startem modelu. Panel pokazuje osobno wyniki USB oraz Wi‑Fi i podaje wybrany transport.
- Niewybrane łącze pozostaje awaryjne dla nowych połączeń. Jeśli wybrana droga nie odpowiada podczas kontroli przed startem, host próbuje drugiej i opisuje zmianę w panelu oraz diagnostyce.
- Dynamiczne przekierowanie ADB jest usuwane po odłączeniu workera lub zamknięciu YourLM; cudze przekierowania ADB nie są usuwane.

## Ważne ograniczenia

- USB przyspiesza tylko transport. Nie zwiększa liczby rdzeni, przepustowości RAM ani możliwości alokacji telefonu.
- Wiele telefonów Redmi ma fizyczne USB 2.0. Dobre Wi‑Fi 5 GHz może uzyskać lepszy wynik i wtedy pozostanie wybrane.
- Odłączenie kabla podczas aktywnego RPC może przerwać bieżące ładowanie lub generowanie. Protokół llama.cpp nie pozwala przenieść rozpoczętej komendy na Wi‑Fi w połowie; fallback działa przy następnym połączeniu.
- Benchmark przesyła 8 MiB na każde badane łącze (zwykle 16 MiB przy porównaniu USB i Wi‑Fi) i może dodać kilka sekund do startu. Na bardzo wolnej lub uszkodzonej drodze obowiązuje ograniczony timeout.

## Kompatybilność i bezpieczeństwo

Identyfikator mostu zmienia się na `b12562-ylm-rpc5`, dlatego APK i EXE muszą pochodzić z RC13. TLS, HMAC ręcznego parowania, przypięcie certyfikatu, limity klientów i lokalny surowy RPC pozostają obowiązkowe. Nie dodano roota ani nowych uprawnień Androida.

## Weryfikacja

CI kompiluje i testuje host na Linux oraz Windows, wykonuje rzeczywisty benchmark przez testowy serwer TLS, sprawdza wybór wyniku ważonego, filtrowanie urządzeń fizycznych i parser dynamicznego portu ADB, kompiluje Java/Android, audytuje podpis APK oraz przeprowadza instalację, ochronę przed downgrade'em i odinstalowanie Windows.
