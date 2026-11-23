## YourLM 0.2.0-rc8 — zapas RAM workera i wspólna diagnostyka Redmi/Windows

RC7 usunął dodatkową pełnotensorową alokację cache, ale rzeczywista próba modelu `huihui-ai-Huihui-gemma-4-26B-A4B-it-abliterated.GGUF` nadal zakończyła się komunikatem:

`ggml-rpc.cpp:699: Remote RPC server crashed or returned malformed response`

To nadal oznacza zerwanie połączenia podczas przesyłania tensora. Ostrzeżenia tokenizera oraz `tensor overrides to CPU ... mmap enabled` nie są błędem końcowym.

### Zmiany w RC8

- Automatyczny podział llama.cpp pozostawia teraz na każdym workerze **1024 MiB zapasu**. RC7 używał `--fit-target 0`, więc planista mógł wypełnić tensorami praktycznie cały RAM zgłoszony jako dostępny i nie zostawić bezpiecznego marginesu dla Androida, mostu TLS, procesu RPC, cache ani nagłej zmiany presji pamięci.
- Wagi nieprzydzielone Redmi nadal pozostają zmapowane przez Windows; model nie jest odrzucany statycznym limitem.
- Po awarii Windows automatycznie otwiera uwierzytelnione połączenie diagnostyczne z Redmi i dołącza do `engine.log` jeden wspólny raport.
- Raport z telefonu obejmuje kod wyjścia i stan natywnego workera, RAM, próg niskiej pamięci, wolne miejsce, rozmiar cache, osierocone pliki, parametry łącza Wi-Fi, log natywny oraz historię przyczyn zakończeń udostępnioną przez Androida.
- Przycisk **Kopiuj diagnostykę** w Windows kopiuje raport hosta i telefonu razem — nie trzeba odblokowywać Redmi po nieudanej próbie.
- Worker pozostawia uwierzytelniony kanał diagnostyczny przez 60 sekund po zakończeniu procesu RPC, aby host zdążył pobrać dane także przy zablokowanym ekranie.
- RC8 wymaga zgodnego workera `b12562-ylm-rpc2`; starsze APK jest odrzucane przed startem modelu.

Pierwszy transfer wag nadal zależy od rzeczywistego pasma Wi-Fi. Raport pokazuje częstotliwość, deklarowaną szybkość łącza i RSSI, co pozwoli odróżnić ograniczenie sieci 2,4 GHz od problemu pamięci lub procesu RPC. Cache pozostaje strumieniowany porcjami do 16 MiB.

### Aktualizacja i próba

Zaktualizuj **oba urządzenia**. Uruchom nowe `YourLM.exe`, w zakładce **Telefon USB** wybierz **Zainstaluj lub zaktualizuj**, uruchom worker na Redmi, usuń poprzedni wpis telefonu i sparuj go nowym kodem. Następnie uruchom model ponownie. Jeśli próba się nie powiedzie, w Windows wybierz **Kopiuj diagnostykę** i prześlij całość.
