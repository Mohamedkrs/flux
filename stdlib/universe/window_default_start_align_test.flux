package universe_test


import "testing"

option now = () => 2030-01-01T00:00:00Z

inData =
    "
#datatype,string,long,dateTime:RFC3339,double,string,string,string
#group,false,false,false,false,true,true,true
#default,_result,,,,,,
,result,table,_time,_value,_field,_measurement,host
,,0,2018-05-22T19:53:26Z,63.053321838378906,used_percent,mem,host.local
,,0,2018-05-22T19:54:36Z,62.71536350250244,used_percent,mem,host.local
,,0,2018-05-22T19:55:46Z,62.38760948181152,used_percent,mem,host.local
,,0,2018-05-22T19:56:56Z,62.74595260620117,used_percent,mem,host.local
,,0,2018-05-22T19:57:06Z,62.78183460235596,used_percent,mem,host.local
,,0,2018-05-22T19:58:16Z,62.46745586395264,used_percent,mem,host.local
"
outData =
    "
#datatype,string,long,dateTime:RFC3339,dateTime:RFC3339,dateTime:RFC3339,double,string,string,string
#group,false,false,true,true,false,false,true,true,true
#default,_result,,,,,,,,
,result,table,_start,_stop,_time,_value,_field,_measurement,host
,,0,2018-05-22T19:54:00Z,2018-05-22T19:55:00Z,2018-05-22T19:54:36Z,62.71536350250244,used_percent,mem,host.local
,,1,2018-05-22T19:55:00Z,2018-05-22T19:56:00Z,2018-05-22T19:55:46Z,62.38760948181152,used_percent,mem,host.local
,,2,2018-05-22T19:56:00Z,2018-05-22T19:57:00Z,2018-05-22T19:56:56Z,62.74595260620117,used_percent,mem,host.local
,,3,2018-05-22T19:57:00Z,2018-05-22T19:58:00Z,2018-05-22T19:57:06Z,62.78183460235596,used_percent,mem,host.local
,,4,2018-05-22T19:58:00Z,2018-05-22T19:59:00Z,2018-05-22T19:58:16Z,62.46745586395264,used_percent,mem,host.local
"
t_window_default_start_align = (table=<-) =>
    table
        |> range(start: 2018-05-22T19:53:30Z, stop: 2018-05-22T19:59:00Z)
        |> window(every: 1m)

test _window_default_start_align = () =>
    ({input: testing.loadStorage(csv: inData), want: testing.loadMem(csv: outData), fn: t_window_default_start_align})
