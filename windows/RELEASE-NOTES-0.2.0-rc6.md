## YourLM 0.2.0-rc6 — widoczny postęp ładowania GGUF

RC6 dodaje czytelny panel postępu dla długiego startu dużych modeli, w tym konfiguracji Lenovo + Redmi RPC. Nie trzeba już zgadywać na podstawie ostatniej linii surowego logu, czy silnik nadal pracuje.

### Co się zmieniło

- Pasek pokazuje sześć potwierdzonych etapów startu: uruchomienie silnika, metadane GGUF, plan pamięci, ładowanie/przesyłanie wag, kontekst i finalizację API.
- Etapy są wyraźnie opisane jako orientacyjne. Dokładne `100%` pojawia się dopiero po faktycznej gotowości modelu; aplikacja nie udaje procentu tensorów, którego samodzielny `llama-server` nie udostępnia.
- Podczas ładowania panel mierzy odczyt dysku, zużycie czasu CPU, zmiany logu oraz ruch RPC do i z workera. Dzięki temu pokazuje, kiedy model rzeczywiście nadal pracuje.
- Podpowiedź wyjaśnia, czy warto czekać. Dopiero po co najmniej trzech minutach i pełnej minucie bez mierzalnej aktywności sugeruje zatrzymanie i próbę bez workera.
- Dodano bezpośredni przycisk **Zatrzymaj ładowanie**, czas trwania oraz dostępny niżej pełny log diagnostyczny.
- Widok działa w jasnym i ciemnym motywie oraz na wąskim ekranie.

### Ważne

Ostrzeżenie llama.cpp o tensorach CPU z aktywnym mmap nadal jest informacyjne i nie oznacza samo w sobie awarii. RC6 nie wymusza globalnie `--load-mode none`, dzięki czemu nie odbiera Windows możliwości mapowania dużych plików GGUF.

Pierwsze uruchomienie dużego modelu z workerem Wi-Fi nadal może trwać długo. Kolejne starty tego samego modelu mogą skorzystać z trwałej pamięci podręcznej RPC. Fizyczne sprawdzenie konkretnego 26B GGUF na Lenovo + Redmi nadal zależy od rozmiaru pliku, kwantyzacji, dostępnej pamięci i jakości sieci.
