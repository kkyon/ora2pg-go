BINARY := ora2pg-go

.PHONY: tidy build run-table clean

tidy:
	go mod tidy

build:
	go build -o $(BINARY) .

run-table: build
	./$(BINARY) --config ./ora2pg.conf --type TABLE --oracle-host localhost --out ./output/ora2pg-go-TABLE.sql

clean:
	rm -f $(BINARY)
