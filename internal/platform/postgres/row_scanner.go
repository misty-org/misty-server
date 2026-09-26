package db

type rowScanner interface{ Scan(...any) error }
